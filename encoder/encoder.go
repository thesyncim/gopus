// Package encoder implements the unified Opus encoder per RFC 6716.
// It orchestrates SILK and CELT sub-encoders for hybrid mode encoding,
// which combines SILK (0-8kHz) with CELT (8-20kHz) for super-wideband
// and fullband speech encoding.
//
// Reference: RFC 6716 Section 3.2
package encoder

import (
	"errors"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// Mode represents the encoding mode.
type Mode int

const (
	// ModeAuto automatically selects the best mode based on content and bandwidth.
	ModeAuto Mode = iota
	// ModeSILK uses SILK-only encoding (configs 0-11).
	ModeSILK
	// ModeHybrid uses combined SILK+CELT encoding (configs 12-15).
	ModeHybrid
	// ModeCELT uses CELT-only encoding (configs 16-31).
	ModeCELT
)

// Errors for the encoder.
var (
	// ErrInvalidSampleRate indicates an unsupported sample rate.
	ErrInvalidSampleRate = errors.New("encoder: invalid sample rate (must be 8000, 12000, 16000, 24000, or 48000)")

	// ErrInvalidChannels indicates an unsupported channel count.
	ErrInvalidChannels = errors.New("encoder: invalid channels (must be 1 or 2)")

	// ErrInvalidFrameSize indicates an unsupported frame size.
	ErrInvalidFrameSize = errors.New("encoder: invalid frame size")

	// ErrInvalidHybridFrameSize indicates a frame size invalid for hybrid mode.
	ErrInvalidHybridFrameSize = errors.New("encoder: hybrid mode only supports 10ms (480) or 20ms (960) frames")

	// ErrEncodingFailed indicates a general encoding failure.
	ErrEncodingFailed = errors.New("encoder: encoding failed")
)

// Encoder is the unified Opus encoder that orchestrates SILK and CELT sub-encoders.
type Encoder struct {
	// Sub-encoders (created lazily)
	silkEncoder     *silk.Encoder
	silkSideEncoder *silk.Encoder // For stereo side channel in hybrid mode
	celtEncoder     *celt.Encoder

	// Configuration
	mode       Mode
	bandwidth  types.Bandwidth
	sampleRate int
	channels   int
	frameSize  int // In samples at 48kHz

	// Bitrate controls
	bitrateMode BitrateMode
	bitrate     int // Target bits per second

	// FEC controls
	fecEnabled                  bool
	packetLoss                  int // Expected packet loss percentage (0-100)
	lastVADActivityQ8           int
	lastVADInputTiltQ15         int
	lastVADInputQualityBandsQ15 [4]int
	lastVADActive               bool
	lastVADValid                bool
	silkVAD                     *VADState
	silkVADSide                 *VADState
	fec                         *fecState

	// DTX (Discontinuous Transmission) controls
	dtxEnabled bool
	dtx        *dtxState
	rng        uint32 // RNG for comfort noise

	// Complexity control (0-10, higher = better quality but slower)
	complexity int

	// Signal type hint for mode selection
	signalType types.Signal

	// Maximum bandwidth limit (actual bandwidth is clamped to this)
	maxBandwidth types.Bandwidth

	// Force channels (-1=auto, 1=mono, 2=stereo)
	forceChannels int

	// LSB depth of input signal (8-24 bits, affects DTX sensitivity)
	lsbDepth int

	// Prediction disabled (reduces inter-frame dependency for error resilience)
	predictionDisabled bool

	// Phase inversion disabled (for stereo decorrelation)
	phaseInversionDisabled bool

	// DC rejection filter state
	hpMem [4]float64

	// Encoder state for CELT delay compensation
	prevSamples []float64

	// Hybrid mode state for improved SILK/CELT coordination
	hybridState *HybridState

	// Audio scene analyzer (The "Brain")
	analyzer *TonalityAnalysisState

	inputBuffer []float64
	delayBuffer []float64

	// SILK downsampling
	silkResampler        *silk.DownsamplingResampler
	silkResamplerRight   *silk.DownsamplingResampler
	silkResamplerRate    int
	silkResampled        []float32
	silkResampledR       []float32
	silkResampledBuffer  []float32
	silkResampledRBuffer []float32

	// Scratch buffers for zero-allocation encoding
	scratchDCPCM      []float64 // DC rejected PCM buffer
	scratchPCM32      []float32 // float64 to float32 conversion buffer
	scratchLeft       []float32 // Left channel deinterleave buffer
	scratchRight      []float32 // Right channel deinterleave buffer
	scratchMono       []float32 // Mono mix buffer (VAD)
	scratchVADFlags   [silk.MaxFramesPerPacket]bool
	scratchPacket     []byte    // Output packet buffer
	scratchDelayedPCM []float64 // Delay-compensated CELT input
	scratchDelayTail  []float64 // Snapshot of delay buffer tail
}

// NewEncoder creates a new unified Opus encoder.
func NewEncoder(sampleRate, channels int) *Encoder {
	validRates := map[int]bool{8000: true, 12000: true, 16000: true, 24000: true, 48000: true}
	if !validRates[sampleRate] {
		sampleRate = 48000
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	maxSamples := 2880 * channels

	return &Encoder{
		mode:                   ModeAuto,
		bandwidth:              types.BandwidthFullband,
		sampleRate:             sampleRate,
		channels:               channels,
		frameSize:              960,
		bitrateMode:            ModeVBR,
		bitrate:                64000,
		fecEnabled:             false,
		packetLoss:             0,
		fec:                    newFECState(),
		dtxEnabled:             false,
		dtx:                    newDTXState(),
		rng:                    22222,
		complexity:             10,
		signalType:             types.SignalAuto,
		maxBandwidth:           types.BandwidthFullband,
		forceChannels:          -1,
		lsbDepth:               24,
		predictionDisabled:     false,
		phaseInversionDisabled: false,
		prevSamples:            make([]float64, 130*channels),
		analyzer:               NewTonalityAnalysisState(sampleRate),
		scratchPCM32:           make([]float32, maxSamples),
		scratchLeft:            make([]float32, 2880),
		scratchRight:           make([]float32, 2880),
		scratchMono:            make([]float32, 2880),
		scratchPacket:          make([]byte, 1276),
	}
}

// SetMode sets the encoding mode.
func (e *Encoder) SetMode(mode Mode) {
	e.mode = mode
}

// Mode returns the current encoding mode.
func (e *Encoder) Mode() Mode {
	return e.mode
}

// SetBandwidth sets the target audio bandwidth.
func (e *Encoder) SetBandwidth(bandwidth types.Bandwidth) {
	e.bandwidth = bandwidth
}

// Bandwidth returns the current bandwidth setting.
func (e *Encoder) Bandwidth() types.Bandwidth {
	return e.bandwidth
}

// SetFrameSize sets the frame size in samples at 48kHz.
func (e *Encoder) SetFrameSize(frameSize int) {
	e.frameSize = frameSize
}

// FrameSize returns the current frame size in samples at 48kHz.
func (e *Encoder) FrameSize() int {
	return e.frameSize
}

// Channels returns the number of audio channels (1 or 2).
func (e *Encoder) Channels() int {
	return e.channels
}

// SampleRate returns the input sample rate.
func (e *Encoder) SampleRate() int {
	return e.sampleRate
}

// Reset clears the encoder state for a new stream.
func (e *Encoder) Reset() {
	for i := range e.prevSamples {
		e.prevSamples[i] = 0
	}
	if len(e.delayBuffer) > 0 {
		clear(e.delayBuffer)
	}
	if len(e.inputBuffer) > 0 {
		e.inputBuffer = e.inputBuffer[:0]
	}
	if e.silkEncoder != nil {
		e.silkEncoder.Reset()
	}
	if e.silkSideEncoder != nil {
		e.silkSideEncoder.Reset()
	}
	if e.celtEncoder != nil {
		e.celtEncoder.Reset()
	}
	e.resetFECState()
	if e.dtx != nil {
		e.dtx.reset()
	}
	if e.analyzer != nil {
		e.analyzer.Reset()
	}
}

// SetFEC enables or disables in-band Forward Error Correction.
func (e *Encoder) SetFEC(enabled bool) {
	e.fecEnabled = enabled
	if enabled && e.fec == nil {
		e.fec = newFECState()
	}
}

// FECEnabled returns whether FEC is enabled.
func (e *Encoder) FECEnabled() bool {
	return e.fecEnabled
}

// SetPacketLoss sets the expected packet loss percentage (0-100).
func (e *Encoder) SetPacketLoss(lossPercent int) {
	if lossPercent < 0 {
		lossPercent = 0
	}
	if lossPercent > 100 {
		lossPercent = 100
	}
	e.packetLoss = lossPercent
	if e.celtEncoder != nil {
		e.celtEncoder.SetPacketLoss(e.packetLoss)
	}
}

// PacketLoss returns the expected packet loss percentage.
func (e *Encoder) PacketLoss() int {
	return e.packetLoss
}

// SetDTX enables or disables Discontinuous Transmission.
func (e *Encoder) SetDTX(enabled bool) {
	e.dtxEnabled = enabled
	if enabled && e.dtx == nil {
		e.dtx = newDTXState()
	}
}

// DTXEnabled returns whether DTX is enabled.
func (e *Encoder) DTXEnabled() bool {
	return e.dtxEnabled
}

// SetComplexity sets encoder complexity (0-10).
func (e *Encoder) SetComplexity(complexity int) {
	if complexity < 0 {
		complexity = 0
	}
	if complexity > 10 {
		complexity = 10
	}
	e.complexity = complexity
	if e.celtEncoder != nil {
		e.celtEncoder.SetComplexity(complexity)
	}
	if e.silkEncoder != nil {
		e.silkEncoder.SetComplexity(complexity)
	}
	if e.silkSideEncoder != nil {
		e.silkSideEncoder.SetComplexity(complexity)
	}
}

// Complexity returns the current complexity setting.
func (e *Encoder) Complexity() int {
	return e.complexity
}

// FinalRange returns the final range coder state after encoding.
func (e *Encoder) FinalRange() uint32 {
	if e.celtEncoder != nil {
		return e.celtEncoder.FinalRange()
	}
	if e.silkEncoder != nil {
		return e.silkEncoder.FinalRange()
	}
	return 0
}

// SetBitrateMode sets the bitrate mode (VBR, CVBR, or CBR).
func (e *Encoder) SetBitrateMode(mode BitrateMode) {
	e.bitrateMode = mode
}

// BitrateMode returns the current bitrate mode.
func (e *Encoder) GetBitrateMode() BitrateMode {
	return e.bitrateMode
}

// SetBitrate sets the target bitrate in bits per second.
func (e *Encoder) SetBitrate(bitrate int) {
	e.bitrate = ClampBitrate(bitrate)
}

// Bitrate returns the current target bitrate.
func (e *Encoder) Bitrate() int {
	return e.bitrate
}

func bitrateToBits(bitrate int, frameSize int) int {
	return (bitrate * frameSize) / 48000
}

func bitsToBitrate(bits int, frameSize int) int {
	return (bits * 48000) / frameSize
}

// computeEquivRate calculates the equivalent bitrate based on frame rate, VBR mode,
// complexity, and packet loss. Matches libopus compute_equiv_rate().
func (e *Encoder) computeEquivRate(bitrate int, channels int, frameRate int, vbr bool, actualMode Mode, complexity int, loss int) int {
	equiv := bitrate
	if frameRate > 50 {
		equiv -= (40*channels + 20) * (frameRate - 50)
	}
	if !vbr {
		equiv -= equiv / 12
	}
	equiv = (equiv * (90 + complexity)) / 100
	if actualMode == ModeSILK || actualMode == ModeHybrid {
		if complexity < 2 {
			equiv = (equiv * 4) / 5
		}
		if loss > 0 {
			equiv -= (equiv * loss) / (6*loss + 10)
		}
	} else if actualMode == ModeCELT {
		if complexity < 5 {
			equiv = (equiv * 9) / 10
		}
	}
	if equiv < 5000 {
		equiv = 5000
	}
	return equiv
}

// computePacketSize determines target packet size based on mode.
func (e *Encoder) computePacketSize(frameSize int, actualMode Mode) int {
	if actualMode == ModeSILK && e.bitrateMode == ModeVBR {
		frameRate := 48000 / frameSize
		equivRate := e.computeEquivRate(e.bitrate, e.channels, frameRate, true, actualMode, e.complexity, e.packetLoss)
		return equivRate
	}
	return e.bitrate
}

// Encode encodes a frame of PCM audio to an Opus packet.
func (e *Encoder) Encode(pcm []float64, frameSize int) ([]byte, error) {
	expectedLen := frameSize * e.channels
	if len(pcm) != expectedLen {
		return nil, ErrInvalidFrameSize
	}
	lookaheadSamples := 0
	pcm = e.dcReject(pcm, frameSize)
	e.inputBuffer = append(e.inputBuffer, pcm...)
	samplesNeeded := (frameSize * e.channels) + lookaheadSamples
	if len(e.inputBuffer) < samplesNeeded {
		return nil, nil
	}
	frameEnd := frameSize * e.channels
	framePCM := e.inputBuffer[:frameEnd]
	lookaheadSlice := e.inputBuffer[frameEnd : frameEnd+lookaheadSamples]

	suppressFrame, sendComfortNoise := e.shouldUseDTX(framePCM)
	if suppressFrame {
		e.inputBuffer = e.inputBuffer[frameEnd:]
		if sendComfortNoise {
			return e.encodeComfortNoise(frameSize)
		}
		return nil, nil
	}

	signalHint := e.signalType
	if signalHint == types.SignalAuto {
		signalHint = e.autoSignalFromPCM(framePCM, frameSize)
	}
	actualMode := e.selectMode(frameSize, signalHint)

	targetBitrate := e.computePacketSize(frameSize, actualMode)
	if actualMode == ModeSILK {
		e.ensureSILKEncoder()
		e.silkEncoder.SetMaxBits(bitrateToBits(targetBitrate, frameSize))
	}

	var frameData []byte
	var err error
	switch actualMode {
	case ModeSILK:
		frameData, err = e.encodeSILKFrame(framePCM, lookaheadSlice, frameSize)
		e.updateDelayBuffer(framePCM, frameSize)
	case ModeHybrid:
		celtPCM := e.applyDelayCompensation(framePCM, frameSize)
		frameData, err = e.encodeHybridFrame(framePCM, celtPCM, lookaheadSlice, frameSize)
	case ModeCELT:
		celtPCM := e.applyDelayCompensation(framePCM, frameSize)
		frameData, err = e.encodeCELTFrame(celtPCM, frameSize)
	default:
		return nil, ErrEncodingFailed
	}
	if err != nil {
		return nil, err
	}
	e.inputBuffer = e.inputBuffer[frameEnd:]
	stereo := e.channels == 2
	packetBW := e.effectiveBandwidth()
	if actualMode == ModeSILK && packetBW > types.BandwidthWideband {
		packetBW = types.BandwidthWideband
	}
	packetLen, err := BuildPacketInto(e.scratchPacket, frameData, modeToTypes(actualMode), packetBW, frameSize, stereo)
	if err != nil {
		return nil, err
	}
	packet := e.scratchPacket[:packetLen]
	switch e.bitrateMode {
	case ModeCBR:
		packet = padToSize(packet, targetBytesForBitrate(e.bitrate, frameSize))
	case ModeCVBR:
		packet = constrainSize(packet, targetBytesForBitrate(e.bitrate, frameSize), CVBRTolerance)
	}
	return packet, nil
}

// modeToTypes converts internal encoder Mode to types.Mode.
func modeToTypes(m Mode) types.Mode {
	switch m {
	case ModeSILK:
		return types.ModeSILK
	case ModeHybrid:
		return types.ModeHybrid
	case ModeCELT:
		return types.ModeCELT
	default:
		return types.ModeCELT
	}
}

// dcReject applies a DC rejection filter (1st-order high-pass filter at 3Hz).
func (e *Encoder) dcReject(in []float64, frameSize int) []float64 {
	channels := e.channels
	n := frameSize * channels
	out := e.ensureDCPCM(n)
	const coef = 0.00039375
	const coef2 = 1.0 - coef
	if channels == 2 {
		m0 := e.hpMem[0]
		m2 := e.hpMem[2]
		for i := 0; i < frameSize; i++ {
			x0 := in[2*i]
			x1 := in[2*i+1]
			out0 := x0 - m0
			out1 := x1 - m2
			m0 = coef*x0 + coef2*m0
			m2 = coef*x1 + coef2*m2
			out[2*i] = out0
			out[2*i+1] = out1
		}
		e.hpMem[0] = m0
		e.hpMem[2] = m2
	} else {
		m0 := e.hpMem[0]
		for i := 0; i < n; i++ {
			x := in[i]
			y := x - m0
			m0 = coef*x + coef2*m0
			out[i] = y
		}
		e.hpMem[0] = m0
	}
	return out
}

func (e *Encoder) ensureDCPCM(size int) []float64 {
	if cap(e.scratchDCPCM) < size {
		e.scratchDCPCM = make([]float64, size)
	}
	return e.scratchDCPCM[:size]
}

func (e *Encoder) ensureDelayedPCM(size int) []float64 {
	if cap(e.scratchDelayedPCM) < size {
		e.scratchDelayedPCM = make([]float64, size)
	}
	return e.scratchDelayedPCM[:size]
}

func (e *Encoder) ensureDelayTail(size int) []float64 {
	if cap(e.scratchDelayTail) < size {
		e.scratchDelayTail = make([]float64, size)
	}
	return e.scratchDelayTail[:size]
}

// applyDelayCompensation prepends the Opus delay buffer (Fs/250) to the current frame
// and returns a frame-sized slice for CELT processing. The delay buffer is updated
// with the latest samples after constructing the output.
func (e *Encoder) applyDelayCompensation(pcm []float64, frameSize int) []float64 {
	delayComp := e.sampleRate / 250
	if delayComp <= 0 {
		return pcm
	}
	channels := e.channels
	if channels < 1 {
		channels = 1
	}
	delaySamples := delayComp * channels
	encoderBufferSamples := (e.sampleRate / 100) * channels
	frameSamples := frameSize * channels
	if len(pcm) < frameSamples {
		frameSamples = len(pcm)
	}
	if delaySamples <= 0 || frameSamples <= 0 {
		return pcm
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}
	if len(e.delayBuffer) != encoderBufferSamples {
		e.delayBuffer = make([]float64, encoderBufferSamples)
	}

	tailStart := encoderBufferSamples - delaySamples
	tail := e.ensureDelayTail(delaySamples)
	copy(tail, e.delayBuffer[tailStart:])

	out := e.ensureDelayedPCM(frameSize * channels)
	if frameSamples <= delaySamples {
		copy(out, tail[:frameSamples])
	} else {
		copy(out, tail)
		copy(out[delaySamples:], pcm[:frameSamples-delaySamples])
	}

	e.updateDelayBufferInternal(pcm, frameSamples, delaySamples, encoderBufferSamples, tail)
	return out
}

// updateDelayBuffer advances the delay buffer without generating a compensated frame.
// This keeps the delay history in sync during SILK-only frames.
func (e *Encoder) updateDelayBuffer(pcm []float64, frameSize int) {
	delayComp := e.sampleRate / 250
	if delayComp <= 0 {
		return
	}
	channels := e.channels
	if channels < 1 {
		channels = 1
	}
	delaySamples := delayComp * channels
	encoderBufferSamples := (e.sampleRate / 100) * channels
	frameSamples := frameSize * channels
	if len(pcm) < frameSamples {
		frameSamples = len(pcm)
	}
	if delaySamples <= 0 || frameSamples <= 0 {
		return
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}
	if len(e.delayBuffer) != encoderBufferSamples {
		e.delayBuffer = make([]float64, encoderBufferSamples)
	}
	tailStart := encoderBufferSamples - delaySamples
	tail := e.ensureDelayTail(delaySamples)
	copy(tail, e.delayBuffer[tailStart:])
	e.updateDelayBufferInternal(pcm, frameSamples, delaySamples, encoderBufferSamples, tail)
}

func (e *Encoder) updateDelayBufferInternal(pcm []float64, frameSamples, delaySamples, encoderBufferSamples int, tail []float64) {
	if delaySamples <= 0 || frameSamples <= 0 {
		return
	}
	if encoderBufferSamples < delaySamples {
		encoderBufferSamples = delaySamples
	}

	if encoderBufferSamples > frameSamples+delaySamples {
		keep := encoderBufferSamples - (frameSamples + delaySamples)
		if keep > 0 {
			copy(e.delayBuffer[:keep], e.delayBuffer[frameSamples:frameSamples+keep])
		}
		copy(e.delayBuffer[keep:keep+delaySamples], tail)
		copy(e.delayBuffer[keep+delaySamples:], pcm[:frameSamples])
		return
	}

	start := delaySamples + frameSamples - encoderBufferSamples
	if start < delaySamples {
		nTail := delaySamples - start
		if nTail > encoderBufferSamples {
			nTail = encoderBufferSamples
		}
		copy(e.delayBuffer[:nTail], tail[start:start+nTail])
		remaining := encoderBufferSamples - nTail
		if remaining > 0 {
			copy(e.delayBuffer[nTail:], pcm[:remaining])
		}
		return
	}

	pcmStart := start - delaySamples
	if pcmStart < 0 {
		pcmStart = 0
	}
	if pcmStart+encoderBufferSamples > len(pcm) {
		pcmStart = len(pcm) - encoderBufferSamples
		if pcmStart < 0 {
			pcmStart = 0
		}
	}
	copy(e.delayBuffer, pcm[pcmStart:pcmStart+encoderBufferSamples])
}

// selectMode determines the actual encoding mode based on settings and content.
func (e *Encoder) selectMode(frameSize int, signalHint types.Signal) Mode {
	if e.mode != ModeAuto {
		return e.mode
	}
	bw := e.effectiveBandwidth()
	perChanRate := e.bitrate
	if e.channels > 0 {
		perChanRate = e.bitrate / e.channels
	}
	if perChanRate >= 48000 && (bw == types.BandwidthSuperwideband || bw == types.BandwidthFullband) {
		return ModeCELT
	}
	switch signalHint {
	case types.SignalVoice:
		switch bw {
		case types.BandwidthNarrowband, types.BandwidthMediumband, types.BandwidthWideband:
			return ModeSILK
		case types.BandwidthSuperwideband, types.BandwidthFullband:
			if frameSize == 480 || frameSize == 960 {
				return ModeHybrid
			}
			return ModeSILK
		}
	case types.SignalMusic:
		return ModeCELT
	}
	switch bw {
	case types.BandwidthNarrowband, types.BandwidthMediumband, types.BandwidthWideband:
		return ModeSILK
	case types.BandwidthSuperwideband:
		if frameSize == 480 || frameSize == 960 {
			return ModeHybrid
		}
		return ModeCELT
	case types.BandwidthFullband:
		return ModeCELT
	default:
		return ModeCELT
	}
}

// autoSignalFromPCM is kept for backward compatibility but RunAnalysis is preferred.
func (e *Encoder) autoSignalFromPCM(pcm []float64, frameSize int) types.Signal {
	if len(pcm) == 0 || frameSize <= 0 {
		return types.SignalAuto
	}
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}
	signalType, _ := classifySignal(pcm32)
	if signalType == 0 {
		return types.SignalVoice
	}
	channels := e.channels
	if channels < 1 {
		channels = 1
	}
	samples := frameSize
	if samples <= 1 {
		return types.SignalVoice
	}
	var energy, diffEnergy float64
	var prev float64
	for i := 0; i < samples; i++ {
		var s float64
		if channels == 2 {
			idx := i * 2
			if idx+1 >= len(pcm) {
				break
			}
			s = 0.5 * (pcm[idx] + pcm[idx+1])
		} else {
			if i >= len(pcm) {
				break
			}
			s = pcm[i]
		}
		energy += s * s
		if i > 0 {
			d := s - prev
			diffEnergy += d * d
		}
		prev = s
	}
	if energy <= 0 {
		return types.SignalVoice
	}
	ratio := diffEnergy / (energy + 1e-12)
	if ratio > 0.25 {
		return types.SignalMusic
	}
	return types.SignalVoice
}

// effectiveBandwidth returns the actual bandwidth to use, considering maxBandwidth limit.
func (e *Encoder) effectiveBandwidth() types.Bandwidth {
	if e.bandwidth > e.maxBandwidth {
		return e.maxBandwidth
	}
	return e.bandwidth
}

// encodeSILKFrame encodes a frame using SILK-only mode.
func (e *Encoder) encodeSILKFrame(pcm []float64, lookahead []float64, frameSize int) ([]byte, error) {
	e.ensureSILKEncoder()
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}
	var lookahead32 []float32
	if len(lookahead) > 0 {
		start := len(pcm)
		if len(e.scratchPCM32) >= start+len(lookahead) {
			lookahead32 = e.scratchPCM32[start : start+len(lookahead)]
		} else {
			lookahead32 = make([]float32, len(lookahead))
		}
		for i, v := range lookahead {
			lookahead32[i] = float32(v)
		}
	}
	cfg := silk.GetBandwidthConfig(e.silkBandwidth())
	targetRate := cfg.SampleRate
	if targetRate != 48000 {
		e.ensureSILKResampler(targetRate)
	}
	targetSamples := frameSize * targetRate / 48000
	if targetSamples <= 0 {
		targetSamples = len(pcm32)
	}
	if e.channels == 2 {
		perChannelRate := e.bitrate / e.channels
		if perChannelRate > 0 {
			e.silkEncoder.SetBitrate(perChannelRate)
		}
		e.silkEncoder.SetFEC(e.fecEnabled)
		e.silkEncoder.SetPacketLoss(e.packetLoss)
		e.ensureSILKSideEncoder()
		if perChannelRate > 0 {
			e.silkSideEncoder.SetBitrate(perChannelRate)
		}
		e.silkSideEncoder.SetFEC(e.fecEnabled)
		e.silkSideEncoder.SetPacketLoss(e.packetLoss)
		left := e.scratchLeft[:frameSize]
		right := e.scratchRight[:frameSize]
		for i := 0; i < frameSize; i++ {
			left[i] = pcm32[i*2]
			right[i] = pcm32[i*2+1]
		}
		lookaheadSize := len(lookahead32) / 2
		leftLookahead := e.scratchLeft[frameSize : frameSize+lookaheadSize]
		rightLookahead := e.scratchRight[frameSize : frameSize+lookaheadSize]
		for i := 0; i < lookaheadSize; i++ {
			leftLookahead[i] = lookahead32[i*2]
			rightLookahead[i] = lookahead32[i*2+1]
		}
		if targetRate != 48000 {
			leftOut := e.ensureSilkResampled(targetSamples)
			rightOut := e.ensureSilkResampledR(targetSamples)
			nL := e.silkResampler.ProcessInto(left, leftOut)
			nR := e.silkResamplerRight.ProcessInto(right, rightOut)
			if nL < nR {
				rightOut = rightOut[:nL]
				leftOut = leftOut[:nL]
			} else if nR < nL {
				leftOut = leftOut[:nR]
				rightOut = rightOut[:nR]
			}
			left = leftOut
			right = rightOut
		}
		fsKHz := targetRate / 1000
		mono := e.scratchMono[:len(left)]
		for i := 0; i < len(left); i++ {
			mono[i] = (left[i] + right[i]) * 0.5
		}
		vadFlag := e.computeSilkVAD(mono, len(left), fsKHz)
		e.silkEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.silkVAD.InputQualityBandsQ15)
		if e.silkSideEncoder != nil {
			e.silkSideEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.silkVAD.InputQualityBandsQ15)
		}
		return silk.EncodeStereoWithEncoder(e.silkEncoder, e.silkSideEncoder, left, right, e.silkBandwidth(), vadFlag)
	}
	var lookaheadOut []float32
	if targetRate != 48000 {
		out := e.ensureSilkResampled(targetSamples)
		n := e.silkResampler.ProcessInto(pcm32, out)
		if n < len(out) {
			out = out[:n]
		}
		pcm32 = out
		if len(lookahead32) > 0 {
			targetLaSamples := len(lookahead32) * targetRate / 48000
			if len(e.silkResampledBuffer) < targetLaSamples {
				e.silkResampledBuffer = make([]float32, targetLaSamples)
			}
			lookaheadOut = e.silkResampledBuffer[:targetLaSamples]
			state := e.silkResampler.State()
			e.silkResampler.ProcessInto(lookahead32, lookaheadOut)
			e.silkResampler.SetState(state)
		}
	} else {
		lookaheadOut = lookahead32
	}
	if e.bitrate > 0 {
		perChannelRate := e.bitrate / e.channels
		if perChannelRate > 0 {
			e.silkEncoder.SetBitrate(perChannelRate)
		}
	}
	e.silkEncoder.SetFEC(e.fecEnabled)
	e.silkEncoder.SetPacketLoss(e.packetLoss)
	fsKHz := targetRate / 1000
	vadFlags, nFrames := e.computeSilkVADFlags(pcm32, fsKHz)
	e.silkEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.lastVADInputQualityBandsQ15)
	if e.fecEnabled || nFrames > 1 {
		return e.silkEncoder.EncodePacketWithFEC(pcm32, lookaheadOut, vadFlags), nil
	}
	vadFlag := false
	if len(vadFlags) > 0 {
		vadFlag = vadFlags[0]
	}
	res := e.silkEncoder.EncodeFrame(pcm32, lookaheadOut, vadFlag)
	return res, nil
}

// encodeCELTFrame encodes a frame using CELT-only mode.
func (e *Encoder) encodeCELTFrame(pcm []float64, frameSize int) ([]byte, error) {
	e.ensureCELTEncoder()
	e.celtEncoder.SetBitrate(e.bitrate)
	e.celtEncoder.SetHybrid(false)
	e.celtEncoder.SetPacketLoss(e.packetLoss)
	e.celtEncoder.SetLSBDepth(e.lsbDepth)
	switch e.bitrateMode {
	case ModeCBR:
		e.celtEncoder.SetVBR(false)
		e.celtEncoder.SetConstrainedVBR(false)
	case ModeCVBR:
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(true)
	case ModeVBR:
		e.celtEncoder.SetVBR(true)
		e.celtEncoder.SetConstrainedVBR(false)
	}
	return e.celtEncoder.EncodeFrame(pcm, frameSize)
}

// ensureSILKEncoder creates the SILK encoder if it doesn't exist.
func (e *Encoder) ensureSILKEncoder() {
	if e.silkEncoder == nil {
		e.silkEncoder = silk.NewEncoder(e.silkBandwidth())
		e.silkEncoder.SetComplexity(e.complexity)
	}
}

// ensureSILKSideEncoder creates the SILK side channel encoder for stereo hybrid mode.
func (e *Encoder) ensureSILKSideEncoder() {
	if e.silkSideEncoder == nil && e.channels == 2 {
		e.silkSideEncoder = silk.NewEncoder(e.silkBandwidth())
		e.silkSideEncoder.SetComplexity(e.complexity)
	}
}

func (e *Encoder) ensureSILKResampler(rate int) {
	if rate <= 0 {
		return
	}
	if e.silkResampler == nil || e.silkResamplerRate != rate {
		e.silkResampler = silk.NewDownsamplingResampler(48000, rate)
		e.silkResamplerRate = rate
		e.silkResamplerRight = nil
		if e.channels == 2 {
			e.silkResamplerRight = silk.NewDownsamplingResampler(48000, rate)
		}
		return
	}
	if e.channels == 2 && e.silkResamplerRight == nil {
		e.silkResamplerRight = silk.NewDownsamplingResampler(48000, rate)
	}
}

func (e *Encoder) ensureSilkVAD() {
	if e.silkVAD == nil {
		e.silkVAD = NewVADState()
	}
}

func (e *Encoder) ensureSilkVADSide() {
	if e.silkVADSide == nil {
		e.silkVADSide = NewVADState()
	}
}

func computeSilkVADWithState(state *VADState, mono []float32, frameSamples, fsKHz int) (int, bool) {
	if state == nil || frameSamples <= 0 || fsKHz <= 0 {
		return 0, false
	}
	if len(mono) < frameSamples {
		return 0, false
	}
	return state.GetSpeechActivity(mono, frameSamples, fsKHz)
}

func (e *Encoder) computeSilkVAD(mono []float32, frameSamples, fsKHz int) bool {
	if frameSamples <= 0 || fsKHz <= 0 {
		e.lastVADValid = false
		return false
	}
	e.ensureSilkVAD()
	activityQ8, active := computeSilkVADWithState(e.silkVAD, mono, frameSamples, fsKHz)
	e.lastVADActivityQ8 = activityQ8
	e.lastVADInputTiltQ15 = e.silkVAD.InputTiltQ15
	e.lastVADInputQualityBandsQ15 = e.silkVAD.InputQualityBandsQ15
	e.lastVADActive = active
	e.lastVADValid = true
	return active
}

func (e *Encoder) computeSilkVADSide(mono []float32, frameSamples, fsKHz int) bool {
	if frameSamples <= 0 || fsKHz <= 0 {
		return false
	}
	e.ensureSilkVADSide()
	activityQ8, active := computeSilkVADWithState(e.silkVADSide, mono, frameSamples, fsKHz)
	_ = activityQ8
	return active
}

func computeSilkFrameLayout(pcmLen, fsKHz int) (frameSamples, nFrames int) {
	if pcmLen <= 0 || fsKHz <= 0 {
		return 0, 0
	}
	frameSamples = fsKHz * 20
	if frameSamples <= 0 {
		return 0, 0
	}
	if pcmLen < frameSamples {
		frameSamples = pcmLen
	}
	nFrames = pcmLen / frameSamples
	if nFrames < 1 {
		nFrames = 1
	}
	if nFrames > silk.MaxFramesPerPacket {
		nFrames = silk.MaxFramesPerPacket
	}
	return frameSamples, nFrames
}

func (e *Encoder) computeSilkVADFlags(pcm []float32, fsKHz int) ([]bool, int) {
	frameSamples, nFrames := computeSilkFrameLayout(len(pcm), fsKHz)
	if nFrames == 0 {
		e.lastVADValid = false
		return nil, 0
	}
	flags := e.scratchVADFlags[:nFrames]
	for i := 0; i < nFrames; i++ {
		start := i * frameSamples
		end := start + frameSamples
		if end > len(pcm) {
			end = len(pcm)
		}
		framePCM := pcm[start:end]
		flags[i] = e.computeSilkVAD(framePCM, len(framePCM), fsKHz)
	}
	return flags, nFrames
}

func (e *Encoder) ensureSilkResampled(size int) []float32 {
	if size <= 0 {
		return nil
	}
	if cap(e.silkResampled) < size {
		e.silkResampled = make([]float32, size)
	}
	return e.silkResampled[:size]
}

func (e *Encoder) ensureSilkResampledR(size int) []float32 {
	if size <= 0 {
		return nil
	}
	if cap(e.silkResampledR) < size {
		e.silkResampledR = make([]float32, size)
	}
	return e.silkResampledR[:size]
}

// ensureCELTEncoder creates the CELT encoder if it doesn't exist.
func (e *Encoder) ensureCELTEncoder() {
	if e.celtEncoder == nil {
		e.celtEncoder = celt.NewEncoder(e.channels)
		e.celtEncoder.SetComplexity(e.complexity)
		// Opus encoder already applies dc_reject at the top level.
		e.celtEncoder.SetDCRejectEnabled(false)
	}
}

// silkBandwidth converts the Opus bandwidth to SILK bandwidth.
func (e *Encoder) silkBandwidth() silk.Bandwidth {
	switch e.bandwidth {
	case types.BandwidthNarrowband:
		return silk.BandwidthNarrowband
	case types.BandwidthMediumband:
		return silk.BandwidthMediumband
	case types.BandwidthWideband:
		return silk.BandwidthWideband
	case types.BandwidthSuperwideband, types.BandwidthFullband:
		return silk.BandwidthWideband
	default:
		return silk.BandwidthWideband
	}
}

// ValidFrameSize returns true if the frame size is valid for the given mode.
func ValidFrameSize(frameSize int, mode Mode) bool {
	switch mode {
	case ModeSILK:
		return frameSize == 480 || frameSize == 960 || frameSize == 1920 || frameSize == 2880
	case ModeHybrid:
		return frameSize == 480 || frameSize == 960
	case ModeCELT:
		return frameSize == 120 || frameSize == 240 || frameSize == 480 || frameSize == 960
	default:
		return frameSize == 120 || frameSize == 240 || frameSize == 480 ||
			frameSize == 960 || frameSize == 1920 || frameSize == 2880
	}
}

// SetSignalType sets the signal type hint for mode selection.
func (e *Encoder) SetSignalType(signal types.Signal) {
	e.signalType = signal
}

// SignalType returns the current signal type hint.
func (e *Encoder) SignalType() types.Signal {
	return e.signalType
}

// SetMaxBandwidth sets the maximum bandwidth limit.
func (e *Encoder) SetMaxBandwidth(bw types.Bandwidth) {
	e.maxBandwidth = bw
}

// MaxBandwidth returns the maximum bandwidth limit.
func (e *Encoder) MaxBandwidth() types.Bandwidth {
	return e.maxBandwidth
}

// SetForceChannels sets the forced channel count.
func (e *Encoder) SetForceChannels(channels int) {
	e.forceChannels = channels
}

// ForceChannels returns the forced channel count (-1 = auto).
func (e *Encoder) ForceChannels() int {
	return e.forceChannels
}

// Lookahead returns the encoder's algorithmic delay in samples at 48kHz.
func (e *Encoder) Lookahead() int {
	baseLookahead := e.sampleRate / 400
	// libopus: delay_compensation = Fs/250 (4 ms)
	delayComp := e.sampleRate / 250
	return baseLookahead + delayComp
}

// SetLSBDepth sets the input signal's LSB depth (8-24 bits).
func (e *Encoder) SetLSBDepth(depth int) {
	if depth < 8 {
		depth = 8
	}
	if depth > 24 {
		depth = 24
	}
	e.lsbDepth = depth
}

// LSBDepth returns the current LSB depth setting.
func (e *Encoder) LSBDepth() int {
	return e.lsbDepth
}

// SetPredictionDisabled disables inter-frame prediction.
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	e.predictionDisabled = disabled
}

// PredictionDisabled returns whether inter-frame prediction is disabled.
func (e *Encoder) PredictionDisabled() bool {
	return e.predictionDisabled
}

// SetPhaseInversionDisabled disables stereo phase inversion.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	e.phaseInversionDisabled = disabled
	if e.celtEncoder != nil {
		e.celtEncoder.SetPhaseInversionDisabled(disabled)
	}
}

// PhaseInversionDisabled returns whether stereo phase inversion is disabled.
func (e *Encoder) PhaseInversionDisabled() bool {
	return e.phaseInversionDisabled
}
