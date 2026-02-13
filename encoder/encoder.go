// Package encoder implements the unified Opus encoder per RFC 6716.
// It orchestrates SILK and CELT sub-encoders for hybrid mode encoding,
// which combines SILK (0-8kHz) with CELT (8-20kHz) for super-wideband
// and fullband speech encoding.
//
// Reference: RFC 6716 Section 3.2
package encoder

import (
	"errors"
	"math"

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
	silkTrace       *silk.EncoderTrace
	celtEncoder     *celt.Encoder
	celtStatsHook   func(celt.CeltTargetStats)

	// Configuration
	mode       Mode
	bandwidth  types.Bandwidth
	sampleRate int
	channels   int
	frameSize  int // In samples at 48kHz
	lowDelay   bool

	// Bitrate controls
	bitrateMode   BitrateMode
	useVBR        bool
	vbrConstraint bool
	bitrate       int // Target bits per second

	// FEC controls
	fecEnabled                  bool
	packetLoss                  int // Expected packet loss percentage (0-100)
	lastVADActivityQ8           int
	lastVADInputTiltQ15         int
	lastVADInputQualityBandsQ15 [4]int
	lastVADActive               bool
	lastVADValid                bool
	lastOpusVADActive           bool
	lastOpusVADValid            bool
	lastOpusVADProb             float32
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

	// LFE mode flag.
	// When true, force CELT-only narrowband behavior for this stream.
	lfe bool

	// LSB depth of input signal (8-24 bits, affects DTX sensitivity)
	lsbDepth int

	// Prediction disabled (reduces inter-frame dependency for error resilience)
	predictionDisabled bool

	// Phase inversion disabled (for stereo decorrelation)
	phaseInversionDisabled bool

	// celtSurroundTrim carries multistream surround-trim bias into CELT alloc-trim.
	celtSurroundTrim float64

	// DC rejection filter state
	hpMem [4]float32

	// Encoder state for CELT delay compensation
	prevSamples []float64

	// Hybrid mode state for improved SILK/CELT coordination
	hybridState *HybridState

	// Audio scene analyzer (The "Brain")
	analyzer *TonalityAnalysisState
	// Last frame analysis info from RunAnalysis(), used by mode heuristics.
	lastAnalysisInfo    AnalysisInfo
	lastAnalysisValid   bool
	lastAnalysisFresh   bool
	prevLongSWBAutoMode Mode
	prevSWB10AutoMode   Mode
	swb10TransientScore int
	prevSWB20AutoMode   Mode
	swb20ModeHoldFrames int

	inputBuffer []float64
	delayBuffer []float64

	// SILK downsampling
	silkResampler       *silk.DownsamplingResampler
	silkResamplerRight  *silk.DownsamplingResampler
	silkResamplerRate   int
	silkResampled       []float32
	silkResampledR      []float32
	silkResampledBuffer []float32
	silkMonoInputHist   [2]float32
	scratchSilkAligned  []float32

	// Scratch buffers for zero-allocation encoding
	scratchDCPCM      []float64 // DC rejected PCM buffer
	scratchPCM32      []float32 // float64 to float32 conversion buffer
	scratchLeft       []float32 // Left channel deinterleave buffer
	scratchRight      []float32 // Right channel deinterleave buffer
	scratchMono       []float32 // Mono mix buffer (VAD)
	scratchVADFlags   [silk.MaxFramesPerPacket]bool
	scratchSideVAD    [silk.MaxFramesPerPacket]bool
	scratchPacket     []byte    // Output packet buffer
	scratchDelayedPCM []float64 // Delay-compensated CELT input
	scratchDelayTail  []float64 // Snapshot of delay buffer tail
	scratchQuantPCM   []float64 // LSB-depth quantized input
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
		lowDelay:               false,
		bitrateMode:            ModeVBR,
		useVBR:                 true,
		vbrConstraint:          false,
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
		prevSWB10AutoMode:      ModeCELT,
		prevSWB20AutoMode:      ModeHybrid,
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

// SetLowDelay toggles low-delay application behavior.
//
// When enabled, CELT delay compensation is disabled to match restricted
// low-delay semantics.
func (e *Encoder) SetLowDelay(enabled bool) {
	e.lowDelay = enabled
}

// LowDelay reports whether low-delay application behavior is enabled.
func (e *Encoder) LowDelay() bool {
	return e.lowDelay
}

// SetBandwidth sets the target audio bandwidth.
func (e *Encoder) SetBandwidth(bandwidth types.Bandwidth) {
	e.bandwidth = bandwidth
	if e.celtEncoder != nil {
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
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
	e.silkMonoInputHist = [2]float32{}
	e.resetFECState()
	if e.dtx != nil {
		e.dtx.reset()
	}
	if e.analyzer != nil {
		e.analyzer.Reset()
	}
	e.lastAnalysisValid = false
	e.lastAnalysisFresh = false
	e.prevSWB10AutoMode = ModeCELT
	e.swb10TransientScore = 0
	e.prevSWB20AutoMode = ModeHybrid
	e.swb20ModeHoldFrames = 0
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
	switch mode {
	case ModeCBR:
		e.useVBR = false
	case ModeCVBR:
		e.useVBR = true
		e.vbrConstraint = true
	case ModeVBR:
		e.useVBR = true
		e.vbrConstraint = false
	default:
		e.useVBR = true
		e.vbrConstraint = false
	}
	e.bitrateMode = modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// BitrateMode returns the current bitrate mode.
func (e *Encoder) GetBitrateMode() BitrateMode {
	return modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// SetVBR enables/disables VBR while preserving the existing constraint setting.
func (e *Encoder) SetVBR(enabled bool) {
	e.useVBR = enabled
	e.bitrateMode = modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// VBR reports whether VBR is enabled.
func (e *Encoder) VBR() bool {
	return e.useVBR
}

// SetVBRConstraint toggles VBR constraint without forcing VBR on/off.
func (e *Encoder) SetVBRConstraint(constrained bool) {
	e.vbrConstraint = constrained
	e.bitrateMode = modeFromVBRFlags(e.useVBR, e.vbrConstraint)
}

// VBRConstraint reports whether constrained VBR is enabled.
func (e *Encoder) VBRConstraint() bool {
	return e.vbrConstraint
}

func modeFromVBRFlags(useVBR, vbrConstraint bool) BitrateMode {
	if !useVBR {
		return ModeCBR
	}
	if vbrConstraint {
		return ModeCVBR
	}
	return ModeVBR
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

// silkInputBitrate mirrors the Opus bits_target reservation before SILK allocation.
// Opus reserves 8 bits for TOC/signaling before deriving the SILK bitrate.
func (e *Encoder) silkInputBitrate(frameSize int) int {
	if e.bitrate <= 0 || frameSize <= 0 {
		return 0
	}
	overheadBps := (8 * 48000) / frameSize
	rate := e.bitrate - overheadBps
	if rate < 0 {
		return 0
	}
	return rate
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
	} else {
		// Mode not known yet: libopus applies half the SILK packet-loss penalty.
		if loss > 0 {
			equiv -= (equiv * loss) / (12*loss + 20)
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
	// Run Opus analysis on the original input frame (before top-level dc_reject
	// and LSB quantization) to match libopus run_analysis ordering.
	rawPCM := pcm
	e.refreshFrameAnalysis(rawPCM, frameSize)
	lookaheadSamples := 0
	pcm = e.quantizeInputToLSBDepth(pcm)
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
		remaining := copy(e.inputBuffer, e.inputBuffer[frameEnd:])
		e.inputBuffer = e.inputBuffer[:remaining]
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
	if e.lfe {
		actualMode = ModeCELT
	}
	if e.mode == ModeAuto &&
		frameSize > 960 &&
		e.effectiveBandwidth() == types.BandwidthSuperwideband &&
		(actualMode == ModeHybrid || actualMode == ModeCELT) {
		e.prevLongSWBAutoMode = actualMode
	}
	if e.mode == ModeAuto &&
		frameSize == 480 &&
		e.effectiveBandwidth() == types.BandwidthSuperwideband &&
		(actualMode == ModeHybrid || actualMode == ModeCELT) {
		e.prevSWB10AutoMode = actualMode
	}
	if e.mode == ModeAuto &&
		frameSize == 960 &&
		e.effectiveBandwidth() == types.BandwidthSuperwideband &&
		(actualMode == ModeHybrid || actualMode == ModeCELT) {
		if e.prevSWB20AutoMode == actualMode && e.swb20ModeHoldFrames > 0 {
			e.swb20ModeHoldFrames++
		} else {
			e.prevSWB20AutoMode = actualMode
			e.swb20ModeHoldFrames = 1
		}
	}

	targetBitrate := e.computePacketSize(frameSize, actualMode)
	if actualMode == ModeSILK {
		e.ensureSILKEncoder()
		e.silkEncoder.SetMaxBits(bitrateToBits(targetBitrate, frameSize))
	}

	var frameData []byte
	var packet []byte
	var err error
	switch actualMode {
	case ModeSILK:
		frameData, err = e.encodeSILKFrame(framePCM, lookaheadSlice, frameSize)
		if err == nil {
			// Match libopus opus_encoder.c SILK-only behavior:
			// strip trailing zero bytes after range coder finalization.
			frameData = trimSilkTrailingZeros(frameData)
		}
		e.updateDelayBuffer(framePCM, frameSize)
	case ModeHybrid:
		celtPCM := e.applyDelayCompensation(framePCM, frameSize)
		if frameSize > 960 {
			packet, err = e.encodeHybridMultiFramePacket(framePCM, celtPCM, lookaheadSlice, frameSize)
		} else {
			frameData, err = e.encodeHybridFrame(framePCM, celtPCM, lookaheadSlice, frameSize)
		}
	case ModeCELT:
		celtPCM := e.prepareCELTPCM(framePCM, frameSize)
		if frameSize > 960 {
			// Long CELT packets are encoded as multi-frame packets.
			packet, err = e.encodeCELTMultiFramePacket(celtPCM, frameSize)
		} else {
			frameData, err = e.encodeCELTFrame(celtPCM, frameSize)
		}
	default:
		return nil, ErrEncodingFailed
	}
	if err != nil {
		return nil, err
	}
	remaining := copy(e.inputBuffer, e.inputBuffer[frameEnd:])
	e.inputBuffer = e.inputBuffer[:remaining]
	if packet == nil {
		stereo := e.channels == 2
		packetBW := e.effectiveBandwidth()
		if actualMode == ModeSILK && packetBW > types.BandwidthWideband {
			packetBW = types.BandwidthWideband
		}
		packetLen, pktErr := BuildPacketInto(e.scratchPacket, frameData, modeToTypes(actualMode), packetBW, frameSize, stereo)
		if pktErr != nil {
			return nil, pktErr
		}
		packet = e.scratchPacket[:packetLen]
	}
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
	fs := e.sampleRate
	if fs <= 0 {
		fs = 48000
	}
	coef := float32(6.3) * float32(3) / float32(fs)
	coef2 := float32(1.0) - coef
	const verySmall = float32(1e-30)
	if channels == 2 {
		m0 := e.hpMem[0]
		m2 := e.hpMem[2]
		for i := 0; i < frameSize; i++ {
			x0 := float32(in[2*i])
			x1 := float32(in[2*i+1])
			out0 := x0 - m0
			out1 := x1 - m2
			m0 = coef*x0 + verySmall + coef2*m0
			m2 = coef*x1 + verySmall + coef2*m2
			out[2*i] = float64(out0)
			out[2*i+1] = float64(out1)
		}
		e.hpMem[0] = m0
		e.hpMem[2] = m2
	} else {
		m0 := e.hpMem[0]
		for i := 0; i < n; i++ {
			x := float32(in[i])
			y := x - m0
			m0 = coef*x + verySmall + coef2*m0
			out[i] = float64(y)
		}
		e.hpMem[0] = m0
	}
	return out
}

func quantizeFloat64ToLSBDepthInPlace(samples []float64, depth int) {
	if depth < 8 {
		depth = 8
	}
	if depth > 24 {
		depth = 24
	}
	scale := math.Ldexp(1.0, depth-1)
	invScale := 1.0 / scale
	for i, v := range samples {
		x := float64(float32(v))
		samples[i] = math.Floor(0.5+x*scale) * invScale
	}
}

func (e *Encoder) quantizeInputToLSBDepth(pcm []float64) []float64 {
	out := e.ensureQuantPCM(len(pcm))
	copy(out, pcm)
	quantizeFloat64ToLSBDepthInPlace(out, e.LSBDepth())
	return out
}

func (e *Encoder) ensureQuantPCM(size int) []float64 {
	if cap(e.scratchQuantPCM) < size {
		e.scratchQuantPCM = make([]float64, size)
	}
	return e.scratchQuantPCM[:size]
}

func (e *Encoder) ensureDCPCM(size int) []float64 {
	if cap(e.scratchDCPCM) < size {
		e.scratchDCPCM = make([]float64, size)
	}
	return e.scratchDCPCM[:size]
}

func trimSilkTrailingZeros(frameData []byte) []byte {
	for len(frameData) > 2 && frameData[len(frameData)-1] == 0 {
		frameData = frameData[:len(frameData)-1]
	}
	return frameData
}

func (e *Encoder) refreshFrameAnalysis(pcm []float64, frameSize int) {
	e.lastAnalysisValid = false
	e.lastAnalysisFresh = false
	if e.analyzer == nil || frameSize <= 0 || len(pcm) == 0 {
		return
	}
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}
	// Keep analysis on float-domain samples to match opus_encode_float / opus_demo -f32.
	info := e.analyzer.RunAnalysis(pcm32, frameSize, e.channels)
	if !info.Valid {
		return
	}
	e.lastAnalysisInfo = info
	e.lastAnalysisValid = true
	e.lastAnalysisFresh = true
}

func (e *Encoder) syncCELTAnalysisToCELT() {
	if e.celtEncoder == nil {
		return
	}
	if !e.lastAnalysisValid {
		e.celtEncoder.SetAnalysisInfo(0, [19]uint8{}, 0, false)
		return
	}
	maybeLogAnalysisDebug(e.celtEncoder.FrameCount(), e.lastAnalysisInfo)
	e.celtEncoder.SetAnalysisInfo(
		e.lastAnalysisInfo.BandwidthIndex,
		e.lastAnalysisInfo.LeakBoost,
		float64(e.lastAnalysisInfo.TonalitySlope),
		true,
	)
}

func quantizeFloat32ToInt16InPlace(samples []float32) {
	const scale = float32(32768.0)
	const invScale = float32(1.0 / 32768.0)
	for i, v := range samples {
		scaled := float64(v * scale)
		if scaled > 32767.0 {
			scaled = 32767.0
		} else if scaled < -32768.0 {
			scaled = -32768.0
		}
		samples[i] = float32(math.RoundToEven(scaled)) * invScale
	}
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

// prepareCELTPCM applies CELT delay compensation unless low-delay mode is active.
func (e *Encoder) prepareCELTPCM(framePCM []float64, frameSize int) []float64 {
	if e.lowDelay {
		return framePCM
	}
	return e.applyDelayCompensation(framePCM, frameSize)
}

// selectMode determines the actual encoding mode based on settings and content.
func (e *Encoder) selectMode(frameSize int, signalHint types.Signal) Mode {
	if frameSize > 960 {
		if e.mode != ModeAuto {
			// Hybrid 40/60ms packets are encoded as 2/3 x 20ms code-3 packets.
			if e.mode == ModeHybrid {
				return ModeHybrid
			}
			// CELT 40/60ms is encoded as multi-frame (2/3 x 20ms) packets.
			return e.mode
		}
		bw := e.effectiveBandwidth()

		// Fullband long frames in auto mode follow CELT-only path in libopus audio app.
		if bw == types.BandwidthFullband {
			return ModeCELT
		}
		if bw == types.BandwidthSuperwideband {
			return e.selectLongSWBAutoMode(frameSize, signalHint)
		}
		// Respect explicit or analyzed signal hints.
		switch signalHint {
		case types.SignalVoice:
			// In SWB long-frame auto mode, libopus only uses Hybrid or CELT.
			// Avoid raw SILK packets in this lane.
			if bw == types.BandwidthSuperwideband {
				return ModeHybrid
			}
			return ModeSILK
		case types.SignalMusic:
			return ModeCELT
		}
		// In auto-signal mode for long frames, bias by bandwidth instead of the
		// per-frame classifier to avoid unstable SILK/CELT switching.
		if bw == types.BandwidthSuperwideband {
			return ModeCELT
		}
		return ModeSILK
	}
	if e.mode != ModeAuto {
		return e.mode
	}
	bw := e.effectiveBandwidth()
	perChanRate := e.bitrate
	if e.channels > 0 {
		perChanRate = e.bitrate / e.channels
	}
	// Keep high-rate Fullband in CELT. For SWB 20ms auto mode, allow
	// signal-driven Hybrid/CELT selection with hysteresis.
	if perChanRate >= 48000 && (bw == types.BandwidthFullband || (bw == types.BandwidthSuperwideband && frameSize != 960 && frameSize != 480)) {
		return ModeCELT
	}

	// Determine the preferred mode based on signal hint and bandwidth.
	preferred := ModeCELT
	switch signalHint {
	case types.SignalVoice:
		switch bw {
		case types.BandwidthNarrowband, types.BandwidthMediumband, types.BandwidthWideband:
			preferred = ModeSILK
		case types.BandwidthSuperwideband, types.BandwidthFullband:
			if frameSize == 480 || frameSize == 960 {
				preferred = ModeHybrid
			} else {
				preferred = ModeSILK
			}
		}
	case types.SignalMusic:
		preferred = ModeCELT
	default:
		switch bw {
		case types.BandwidthNarrowband, types.BandwidthMediumband, types.BandwidthWideband:
			preferred = ModeSILK
		case types.BandwidthSuperwideband:
			if frameSize == 480 || frameSize == 960 {
				preferred = ModeHybrid
			} else {
				preferred = ModeCELT
			}
		case types.BandwidthFullband:
			preferred = ModeCELT
		}
	}

	// Validate that the selected mode supports the requested frame size.
	// If not, fall back to a compatible mode.
	if !ValidFrameSize(frameSize, preferred) {
		if ValidFrameSize(frameSize, ModeCELT) {
			return ModeCELT
		}
		if ValidFrameSize(frameSize, ModeSILK) {
			return ModeSILK
		}
		return ModeCELT
	}
	return preferred
}

// selectLongSWBAutoMode mirrors libopus mode-threshold control for long-frame SWB
// auto mode (Celt-only vs Silk/Hybrid lane), using analysis-derived voice estimate
// and previous-mode hysteresis.
func (e *Encoder) selectLongSWBAutoMode(frameSize int, signalHint types.Signal) Mode {
	frameRate := e.sampleRate / frameSize
	if frameRate <= 0 {
		frameRate = 50
	}
	useVBR := e.bitrateMode != ModeCBR
	equivRate := e.computeEquivRate(e.bitrate, e.channels, frameRate, useVBR, ModeAuto, e.complexity, e.packetLoss)

	voiceEst := 48 // OPUS_APPLICATION_AUDIO default when analysis is unavailable.
	if e.signalType == types.SignalVoice {
		voiceEst = 127
	} else if e.signalType == types.SignalMusic {
		voiceEst = 0
	} else if e.lastAnalysisValid {
		prob := float64(e.lastAnalysisInfo.MusicProb)
		if prob < 0 {
			prob = 0
		}
		if prob > 1 {
			prob = 1
		}
		voiceEst = int(math.Floor(0.5 + prob*127.0))
		// Audio application never assumes >90% speech confidence in auto mode.
		if voiceEst > 115 {
			voiceEst = 115
		}
	} else if signalHint == types.SignalVoice {
		voiceEst = 127
	} else if signalHint == types.SignalMusic {
		voiceEst = 0
	}

	modeVoice := 64000
	if e.channels == 2 {
		modeVoice = 44000
	}
	const modeMusic = 10000
	threshold := modeMusic + (voiceEst*voiceEst*(modeVoice-modeMusic))/16384

	// libopus hysteresis: bias against rapid CELT<->SILK/HYBRID switching.
	if e.prevLongSWBAutoMode == ModeCELT {
		threshold -= 2000
	} else if e.prevLongSWBAutoMode == ModeHybrid {
		threshold += 4000
	}

	// Keep strongly tonal long SWB content in CELT-only mode.
	if e.lastAnalysisValid && e.lastAnalysisInfo.Tonality >= 0.42 {
		return ModeCELT
	}
	if e.lastAnalysisValid &&
		e.lastAnalysisInfo.MusicProb < 0.90 &&
		e.lastAnalysisInfo.Tonality < 0.12 {
		return ModeCELT
	}

	if equivRate >= threshold {
		return ModeCELT
	}
	return ModeHybrid
}

// autoSignalFromPCM is kept for backward compatibility but RunAnalysis is preferred.
func (e *Encoder) autoSignalFromPCM(pcm []float64, frameSize int) types.Signal {
	if len(pcm) == 0 || frameSize <= 0 {
		return types.SignalAuto
	}
	if !e.lastAnalysisFresh {
		pcm32 := e.scratchPCM32[:len(pcm)]
		for i, v := range pcm {
			pcm32[i] = float32(v)
		}
		runAnalyzer := frameSize > 960
		if !runAnalyzer && e.mode == ModeAuto && frameSize == 960 && e.effectiveBandwidth() == types.BandwidthSuperwideband {
			runAnalyzer = true
		}
		if runAnalyzer && e.analyzer != nil {
			info := e.analyzer.RunAnalysis(pcm32, frameSize, e.channels)
			if info.Valid {
				e.lastAnalysisInfo = info
				e.lastAnalysisValid = true
				e.lastAnalysisFresh = true
			}
		}
	}

	// Only trust clear decisions from analysis probabilities on long frames.
	if frameSize > 960 && e.lastAnalysisValid {
		if e.lastAnalysisInfo.MusicProb >= 0.65 {
			return types.SignalMusic
		}
		if e.lastAnalysisInfo.MusicProb <= 0.60 {
			return types.SignalVoice
		}
		return types.SignalAuto
	}
	swb10Auto := e.mode == ModeAuto && frameSize == 480 && e.effectiveBandwidth() == types.BandwidthSuperwideband
	swb20Auto := e.mode == ModeAuto && frameSize == 960 && e.effectiveBandwidth() == types.BandwidthSuperwideband
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}
	signalType, _ := classifySignal(pcm32)
	if signalType == 0 && !swb10Auto && !swb20Auto {
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
	avgEnergy := energy / float64(samples)

	// SWB 10ms transient gate.
	// Keep CELT by default, but allow transition to Hybrid for sustained
	// sparse/transient content where libopus tends to switch later in the run.
	if swb10Auto {
		if ratio >= 0.9 && avgEnergy <= 0.03 {
			e.swb10TransientScore += 2
		} else if ratio >= 0.5 && avgEnergy <= 0.015 {
			e.swb10TransientScore++
		} else {
			e.swb10TransientScore--
			if e.swb10TransientScore < 0 {
				e.swb10TransientScore = 0
			}
		}
		if e.swb10TransientScore > 100 {
			e.swb10TransientScore = 100
		}
		desired := e.prevSWB10AutoMode
		if desired != ModeCELT && desired != ModeHybrid {
			desired = ModeCELT
		}
		if e.swb10TransientScore >= 30 {
			desired = ModeHybrid
		} else if e.swb10TransientScore <= 10 {
			desired = ModeCELT
		}
		if desired == ModeHybrid {
			return types.SignalVoice
		}
		return types.SignalMusic
	}

	// SWB 20ms auto-mode hysteresis.
	// This mirrors libopus-style transition penalties by requiring sustained
	// evidence before switching between CELT and Hybrid.
	if swb20Auto && e.lastAnalysisValid {
		vad := float64(e.lastAnalysisInfo.VADProb)
		music := float64(e.lastAnalysisInfo.MusicProb)
		strongVoice := ratio >= 1.0 && vad >= 0.16
		strongMusic := (vad <= 0.25 && ratio <= 0.05) ||
			(ratio <= 0.06 && vad >= 0.42 && vad <= 0.60 && music <= 0.75)

		prev := e.prevSWB20AutoMode
		if prev != ModeHybrid && prev != ModeCELT {
			prev = ModeHybrid
		}
		desired := prev
		if e.swb20ModeHoldFrames == 0 {
			// Bootstrap SWB20 auto mode from the first analyzed frame.
			if vad <= 0.27 && ratio <= 0.06 {
				desired = ModeCELT
			} else {
				desired = ModeHybrid
			}
		} else if prev == ModeHybrid {
			if strongMusic {
				desired = ModeCELT
			}
		} else if strongVoice {
			desired = ModeHybrid
		}

		// Require ~340 ms of stable mode before allowing a switch.
		if e.swb20ModeHoldFrames > 0 && desired != prev && e.swb20ModeHoldFrames < 17 {
			desired = prev
		}

		if desired == ModeHybrid {
			return types.SignalVoice
		}
		return types.SignalMusic
	}

	if ratio > 0.25 {
		return types.SignalMusic
	}
	return types.SignalVoice
}

// effectiveBandwidth returns the actual bandwidth to use, considering maxBandwidth limit.
func (e *Encoder) effectiveBandwidth() types.Bandwidth {
	if e.lfe {
		return types.BandwidthNarrowband
	}
	if e.bandwidth > e.maxBandwidth {
		return e.maxBandwidth
	}
	return e.bandwidth
}

// encodeSILKFrame encodes a frame using SILK-only mode.
func (e *Encoder) encodeSILKFrame(pcm []float64, lookahead []float64, frameSize int) ([]byte, error) {
	e.ensureSILKEncoder()
	e.updateOpusVAD(pcm, frameSize)
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
	// Match libopus enc_API.c float path: quantize to int16 precision
	// before SILK resampling/input buffering.
	quantizeFloat32ToInt16InPlace(pcm32)
	quantizeFloat32ToInt16InPlace(lookahead32)

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
		// Set bitrates: total rate on mid encoder (StereoLRToMSWithRates splits it),
		// per-channel rate on side encoder for its own SNR control.
		totalSilkRate := e.silkInputBitrate(frameSize)
		perChannelRate := totalSilkRate / e.channels
		if totalSilkRate > 0 {
			e.silkEncoder.SetBitrate(totalSilkRate)
		}
		e.silkEncoder.SetFEC(e.fecEnabled)
		e.silkEncoder.SetPacketLoss(e.packetLoss)
		e.ensureSILKSideEncoder()
		if totalSilkRate > 0 {
			e.silkSideEncoder.SetBitrate(totalSilkRate)
		} else if perChannelRate > 0 {
			e.silkSideEncoder.SetBitrate(perChannelRate)
		}
		e.silkSideEncoder.SetFEC(e.fecEnabled)
		e.silkSideEncoder.SetPacketLoss(e.packetLoss)

		// Set VBR mode on both encoders (matching mono path).
		switch e.bitrateMode {
		case ModeCBR:
			e.silkEncoder.SetVBR(false)
			e.silkSideEncoder.SetVBR(false)
		default:
			e.silkEncoder.SetVBR(true)
			e.silkSideEncoder.SetVBR(true)
		}

		// Set max bits for both encoders.
		if e.bitrate > 0 {
			targetBytes := targetBytesForBitrate(e.bitrate, frameSize)
			maxBytes := targetBytes
			switch e.bitrateMode {
			case ModeVBR:
				maxBytes = maxSilkPacketBytes
			case ModeCVBR:
				maxBytes = int(float64(targetBytes) * (1 + CVBRTolerance))
				if maxBytes < 1 {
					maxBytes = 1
				}
				if maxBytes > maxSilkPacketBytes {
					maxBytes = maxSilkPacketBytes
				}
			}
			e.silkEncoder.SetMaxBits(maxBytes * 8)
			e.silkSideEncoder.SetMaxBits(maxBytes * 8)
		}

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
		vadFlags, _ := e.computeSilkVADFlags(mono, fsKHz)
		var sideVADFlags []bool
		e.silkEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.silkVAD.InputQualityBandsQ15)
		if e.silkSideEncoder != nil {
			// Side channel has different activity/tilt than mid; keep a separate VAD state.
			for i := 0; i < len(left); i++ {
				mono[i] = (left[i] - right[i]) * 0.5
			}
			sideVADFlags, _ = e.computeSilkVADSideFlags(mono, fsKHz)
			if e.silkVADSide != nil {
				e.silkSideEncoder.SetVADState(e.silkVADSide.SpeechActivityQ8, e.silkVADSide.InputTiltQ15, e.silkVADSide.InputQualityBandsQ15)
			} else {
				e.silkSideEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.silkVAD.InputQualityBandsQ15)
			}
		}
		return silk.EncodeStereoWithEncoderVADFlagsWithSide(e.silkEncoder, e.silkSideEncoder, left, right, e.silkBandwidth(), vadFlags, sideVADFlags)
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
	// Match libopus mono SILK handoff for WB/SWB paths where the 16 kHz
	// encoder input uses inputBuf+1 semantics across frames.
	if e.channels == 1 && targetRate == 16000 {
		pcm32 = e.alignSilkMonoInput(pcm32)
	}
	if e.bitrate > 0 {
		perChannelRate := e.silkInputBitrate(frameSize) / e.channels
		if perChannelRate > 0 {
			e.silkEncoder.SetBitrate(perChannelRate)
		}
	}
	switch e.bitrateMode {
	case ModeCBR:
		e.silkEncoder.SetVBR(false)
	default:
		e.silkEncoder.SetVBR(true)
	}
	// Set SILK max bits based on bitrate mode (matches opus_encoder.c behavior).
	if e.bitrate > 0 {
		targetBytes := targetBytesForBitrate(e.bitrate, frameSize)
		maxBytes := targetBytes
		switch e.bitrateMode {
		case ModeVBR:
			maxBytes = maxSilkPacketBytes
		case ModeCVBR:
			maxBytes = int(float64(targetBytes) * (1 + CVBRTolerance))
			if maxBytes < 1 {
				maxBytes = 1
			}
			if maxBytes > maxSilkPacketBytes {
				maxBytes = maxSilkPacketBytes
			}
		case ModeCBR:
			// keep targetBytes
		}
		e.silkEncoder.SetMaxBits(maxBytes * 8)
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
	return e.encodeCELTFrameWithBitrate(pcm, frameSize, e.bitrate)
}

func (e *Encoder) encodeCELTFrameWithBitrate(pcm []float64, frameSize int, bitrate int) ([]byte, error) {
	e.ensureCELTEncoder()
	e.syncCELTAnalysisToCELT()
	e.celtEncoder.SetBitrate(bitrate)
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	e.celtEncoder.SetHybrid(false)
	e.celtEncoder.SetDCRejectEnabled(false)
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

// encodeCELTMultiFramePacket encodes 40/60ms CELT packets by splitting into
// 20ms CELT frames and packing them using code-3 framing.
func (e *Encoder) encodeCELTMultiFramePacket(celtPCM []float64, frameSize int) ([]byte, error) {
	if frameSize <= 960 || frameSize%960 != 0 {
		return nil, ErrInvalidFrameSize
	}
	frameCount := frameSize / 960
	if frameCount < 2 || frameCount > 3 {
		return nil, ErrInvalidFrameSize
	}
	if len(celtPCM) != frameSize*e.channels {
		return nil, ErrInvalidFrameSize
	}

	frameStride := 960 * e.channels
	frames := make([][]byte, frameCount)
	sameSize := true
	prevSize := -1
	subframeBitrate := e.bitrate
	if e.bitrateMode == ModeVBR {
		// For 40/60ms CELT VBR packets, encode each 20ms subframe with a
		// reduced bitrate budget to avoid repeatedly hitting the per-frame
		// CELT VBR boost ceiling across multiple subframes.
		subframeBitrate = (e.bitrate * 3) / 5
		if subframeBitrate < 6000 {
			subframeBitrate = 6000
		}
	}
	for i := 0; i < frameCount; i++ {
		start := i * frameStride
		end := start + frameStride
		frameData, err := e.encodeCELTFrameWithBitrate(celtPCM[start:end], 960, subframeBitrate)
		if err != nil {
			return nil, err
		}
		// Keep a stable copy because the range coder output buffer is reused.
		frameCopy := append([]byte(nil), frameData...)
		frames[i] = frameCopy
		if prevSize >= 0 && len(frameCopy) != prevSize {
			sameSize = false
		}
		prevSize = len(frameCopy)
	}

	return BuildMultiFramePacket(
		frames,
		types.ModeCELT,
		e.effectiveBandwidth(),
		960,
		e.channels == 2,
		!sameSize,
	)
}

// encodeHybridMultiFramePacket encodes 40/60ms hybrid packets by splitting into
// 20ms hybrid frames and packing them using code-3 framing.
func (e *Encoder) encodeHybridMultiFramePacket(pcm []float64, celtPCM []float64, lookahead []float64, frameSize int) ([]byte, error) {
	if frameSize <= 960 || frameSize%960 != 0 {
		return nil, ErrInvalidFrameSize
	}
	frameCount := frameSize / 960
	if frameCount < 2 || frameCount > 3 {
		return nil, ErrInvalidFrameSize
	}
	if len(pcm) != frameSize*e.channels || len(celtPCM) != frameSize*e.channels {
		return nil, ErrInvalidFrameSize
	}

	frameStride := 960 * e.channels
	frames := make([][]byte, frameCount)
	sameSize := true
	prevSize := -1
	for i := 0; i < frameCount; i++ {
		start := i * frameStride
		end := start + frameStride
		subPCM := pcm[start:end]
		subCELTPCM := celtPCM[start:end]

		// Hybrid subframes in multi-frame packets should be encoded exactly like
		// independent 20ms frames. Do not leak future subframe samples as lookahead.
		subLookahead := lookahead

		frameData, err := e.encodeHybridFrame(subPCM, subCELTPCM, subLookahead, 960)
		if err != nil {
			return nil, err
		}
		// Keep a stable copy because encoder scratch buffers are reused.
		frameCopy := append([]byte(nil), frameData...)
		frames[i] = frameCopy
		if prevSize >= 0 && len(frameCopy) != prevSize {
			sameSize = false
		}
		prevSize = len(frameCopy)
	}

	return BuildMultiFramePacket(
		frames,
		types.ModeHybrid,
		e.effectiveBandwidth(),
		960,
		e.channels == 2,
		!sameSize,
	)
}

// ensureSILKEncoder creates the SILK encoder if it doesn't exist.
func (e *Encoder) ensureSILKEncoder() {
	bw := e.silkBandwidth()
	if e.silkEncoder != nil && e.silkEncoder.Bandwidth() == bw {
		return
	}
	e.silkEncoder = silk.NewEncoder(bw)
	e.silkEncoder.SetComplexity(e.complexity)
	e.silkEncoder.SetTrace(e.silkTrace)
	// The WB mono handoff state is specific to 16 kHz SILK input alignment.
	// Reset it whenever the SILK core bandwidth/sample-rate changes.
	e.silkMonoInputHist = [2]float32{}
}

// ensureSILKSideEncoder creates the SILK side channel encoder for stereo hybrid mode.
func (e *Encoder) ensureSILKSideEncoder() {
	if e.channels != 2 {
		return
	}
	bw := e.silkBandwidth()
	if e.silkSideEncoder != nil && e.silkSideEncoder.Bandwidth() == bw {
		return
	}
	e.silkSideEncoder = silk.NewEncoder(bw)
	e.silkSideEncoder.SetComplexity(e.complexity)
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

func (e *Encoder) alignSilkMonoInput(in []float32) []float32 {
	n := len(in)
	if n == 0 {
		return in
	}
	if cap(e.scratchSilkAligned) < n {
		e.scratchSilkAligned = make([]float32, n)
	}
	out := e.scratchSilkAligned[:n]
	out[0] = e.silkMonoInputHist[1]
	if n > 1 {
		copy(out[1:], in[:n-1])
		e.silkMonoInputHist[0] = in[n-2]
		e.silkMonoInputHist[1] = in[n-1]
	} else {
		e.silkMonoInputHist[0] = e.silkMonoInputHist[1]
		e.silkMonoInputHist[1] = in[0]
	}
	return out
}

// updateOpusVAD updates the Opus-level VAD activity state from the tonality analyzer.
// This mirrors opus_encoder.c behavior where SILK VAD is suppressed if Opus VAD is inactive.
func (e *Encoder) updateOpusVAD(pcm []float64, frameSize int) {
	if e.lastAnalysisFresh && e.lastAnalysisValid {
		e.lastAnalysisFresh = false
		e.lastOpusVADProb = e.lastAnalysisInfo.VADProb
		e.lastOpusVADValid = true
		e.lastOpusVADActive = e.lastAnalysisInfo.VADProb >= DTXActivityThreshold
		return
	}
	if e.analyzer == nil || frameSize <= 0 || len(pcm) == 0 {
		e.lastOpusVADValid = false
		e.lastOpusVADActive = true
		e.lastOpusVADProb = 1.0
		return
	}
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}
	info := e.analyzer.RunAnalysis(pcm32, frameSize, e.channels)
	e.lastOpusVADProb = info.VADProb
	e.lastOpusVADValid = info.Valid
	if !info.Valid {
		e.lastOpusVADActive = true
		return
	}
	e.lastOpusVADActive = info.VADProb >= DTXActivityThreshold
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

func (e *Encoder) computeSilkVADSideFlags(pcm []float32, fsKHz int) ([]bool, int) {
	frameSamples, nFrames := computeSilkFrameLayout(len(pcm), fsKHz)
	if nFrames == 0 {
		return nil, 0
	}
	flags := e.scratchSideVAD[:nFrames]
	for i := 0; i < nFrames; i++ {
		start := i * frameSamples
		end := start + frameSamples
		if end > len(pcm) {
			end = len(pcm)
		}
		framePCM := pcm[start:end]
		flags[i] = e.computeSilkVADSide(framePCM, len(framePCM), fsKHz)
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
		e.celtEncoder.SetTargetStatsHook(e.celtStatsHook)
		// Opus encoder already applies dc_reject at the top level.
		e.celtEncoder.SetDCRejectEnabled(false)
		// Opus encoder already applies CELT delay compensation at the top level.
		e.celtEncoder.SetDelayCompensationEnabled(false)
	}
	e.celtEncoder.SetLFE(e.lfe)
	e.celtEncoder.SetSurroundTrim(e.celtSurroundTrim)
	e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
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

// LastSilkVADActivity returns the last SILK VAD speech activity (Q8, 0-255).
func (e *Encoder) LastSilkVADActivity() int {
	return e.lastVADActivityQ8
}

// LastSilkVADInputTiltQ15 returns the last SILK VAD input tilt (Q15).
func (e *Encoder) LastSilkVADInputTiltQ15() int {
	return e.lastVADInputTiltQ15
}

// LastOpusVADProb returns the last Opus-level VAD probability (0..1).
func (e *Encoder) LastOpusVADProb() float32 {
	return e.lastOpusVADProb
}

// LastOpusVADActive returns whether the Opus-level VAD classified the last frame as active.
func (e *Encoder) LastOpusVADActive() bool {
	return e.lastOpusVADActive
}

// LastSilkLTPCorr returns the last SILK pitch correlation estimate.
func (e *Encoder) LastSilkLTPCorr() float32 {
	if e.silkEncoder == nil {
		return 0
	}
	return e.silkEncoder.LTPCorr()
}

// SetSilkTrace enables SILK encoder tracing for parity debugging.
// Only applies when the SILK encoder is active.
func (e *Encoder) SetSilkTrace(trace *silk.EncoderTrace) {
	e.silkTrace = trace
	e.ensureSILKEncoder()
	e.silkEncoder.SetTrace(e.silkTrace)
}

// SetCELTTargetStatsHook installs a callback for per-frame CELT VBR target diagnostics.
// Only applies when the CELT encoder is active.
func (e *Encoder) SetCELTTargetStatsHook(fn func(celt.CeltTargetStats)) {
	e.celtStatsHook = fn
	if e.celtEncoder != nil {
		e.celtEncoder.SetTargetStatsHook(fn)
	}
}

// SetMaxBandwidth sets the maximum bandwidth limit.
func (e *Encoder) SetMaxBandwidth(bw types.Bandwidth) {
	e.maxBandwidth = bw
	if e.celtEncoder != nil {
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
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

// SetLFE enables or disables LFE mode.
func (e *Encoder) SetLFE(enabled bool) {
	e.lfe = enabled
	if e.celtEncoder != nil {
		e.celtEncoder.SetLFE(enabled)
		e.celtEncoder.SetBandwidth(celtBandwidthFromTypes(e.effectiveBandwidth()))
	}
}

// LFE reports whether LFE mode is enabled.
func (e *Encoder) LFE() bool {
	return e.lfe
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

// SetCELTSurroundTrim sets the CELT alloc-trim surround bias.
func (e *Encoder) SetCELTSurroundTrim(trim float64) {
	e.celtSurroundTrim = trim
	if e.celtEncoder != nil {
		e.celtEncoder.SetSurroundTrim(trim)
	}
}

// CELTSurroundTrim returns the current CELT alloc-trim surround bias.
func (e *Encoder) CELTSurroundTrim() float64 {
	return e.celtSurroundTrim
}
