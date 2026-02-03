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
// It supports three encoding modes:
// - ModeSILK: SILK-only for speech at lower bandwidths
// - ModeHybrid: Combined SILK+CELT for speech at SWB/FB
// - ModeCELT: CELT-only for music or high-quality audio
//
// Reference: RFC 6716 Section 3.2
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

	// FEC controls (08-04)
	fecEnabled        bool
	packetLoss        int // Expected packet loss percentage (0-100)
	lastVADActivityQ8 int
	lastVADInputTiltQ15 int
	lastVADInputQualityQ15 int
	lastVADActive     bool
	lastVADValid      bool
	silkVAD           *VADState
	silkVADSide       *VADState
	fec               *fecState

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

	// Encoder state for CELT delay compensation
	// The 2.7ms delay (130 samples at 48kHz) aligns SILK and CELT
	prevSamples []float64

	// Hybrid mode state for improved SILK/CELT coordination
	// Contains HB_gain and crossover energy matching
	hybridState *HybridState

	// SILK downsampling (48kHz -> SILK bandwidth rate) for SILK-only mode
	// Uses DownsamplingResampler with proper AR2+FIR algorithm (not IIR_FIR upsampling)
	silkResampler      *silk.DownsamplingResampler
	silkResamplerRight *silk.DownsamplingResampler
	silkResamplerRate  int
	silkResampled      []float32
	silkResampledR     []float32

	// Scratch buffers for zero-allocation encoding
	scratchPCM32    []float32 // float64 to float32 conversion buffer
	scratchLeft     []float32 // Left channel deinterleave buffer
	scratchRight    []float32 // Right channel deinterleave buffer
	scratchMono     []float32 // Mono mix buffer (VAD)
	scratchVADFlags [silk.MaxFramesPerPacket]bool
	scratchPacket   []byte // Output packet buffer
}

// NewEncoder creates a new unified Opus encoder.
// sampleRate must be one of: 8000, 12000, 16000, 24000, 48000
// channels must be 1 (mono) or 2 (stereo)
//
// The encoder defaults to:
// - ModeAuto (automatic mode selection)
// - BandwidthFullband
// - 20ms frames (960 samples at 48kHz)
func NewEncoder(sampleRate, channels int) *Encoder {
	// Validate sample rate
	validRates := map[int]bool{8000: true, 12000: true, 16000: true, 24000: true, 48000: true}
	if !validRates[sampleRate] {
		sampleRate = 48000 // Default to 48kHz
	}

	// Validate channels
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}

	// Max frame size is 2880 samples (60ms at 48kHz) per channel
	maxSamples := 2880 * channels

	return &Encoder{
		mode:                   ModeAuto,
		bandwidth:              types.BandwidthFullband,
		sampleRate:             sampleRate,
		channels:               channels,
		frameSize:              960,     // Default 20ms
		bitrateMode:            ModeVBR, // VBR is default
		bitrate:                64000,   // 64 kbps default
		fecEnabled:             false,   // FEC disabled by default
		packetLoss:             0,       // 0% packet loss expected
		fec:                    newFECState(),
		dtxEnabled:             false,
		dtx:                    newDTXState(),
		rng:                    22222,                         // Match libopus seed
		complexity:             10,                            // Default: highest quality
		signalType:             types.SignalAuto,              // Auto-detect signal type
		maxBandwidth:           types.BandwidthFullband,       // No bandwidth limit
		forceChannels:          -1,                            // Auto channel selection
		lsbDepth:               24,                            // Full 24-bit depth
		predictionDisabled:     false,                         // Inter-frame prediction enabled
		phaseInversionDisabled: false,                         // Phase inversion enabled for stereo
		prevSamples:            make([]float64, 130*channels), // CELT delay compensation buffer
		scratchPCM32:           make([]float32, maxSamples),   // float64 to float32 conversion
		scratchLeft:            make([]float32, 2880),         // Stereo deinterleave buffer
		scratchRight:           make([]float32, 2880),         // Stereo deinterleave buffer
		scratchMono:            make([]float32, 2880),         // Mono mix buffer for VAD
		scratchPacket:          make([]byte, 1276),            // Max Opus packet (TOC + 1275 payload)
	}
}

// SetMode sets the encoding mode.
// Use ModeAuto for automatic selection based on content and bandwidth.
func (e *Encoder) SetMode(mode Mode) {
	e.mode = mode
}

// Mode returns the current encoding mode.
func (e *Encoder) Mode() Mode {
	return e.mode
}

// SetBandwidth sets the target audio bandwidth.
// The bandwidth affects mode selection in ModeAuto.
func (e *Encoder) SetBandwidth(bandwidth types.Bandwidth) {
	e.bandwidth = bandwidth
}

// Bandwidth returns the current bandwidth setting.
func (e *Encoder) Bandwidth() types.Bandwidth {
	return e.bandwidth
}

// SetFrameSize sets the frame size in samples at 48kHz.
// Valid sizes: 120 (2.5ms), 240 (5ms), 480 (10ms), 960 (20ms), 1920 (40ms), 2880 (60ms)
// Note: Hybrid mode only supports 480 and 960.
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
	// Clear delay compensation buffer
	for i := range e.prevSamples {
		e.prevSamples[i] = 0
	}

	// Reset sub-encoders if they exist
	if e.silkEncoder != nil {
		e.silkEncoder.Reset()
	}
	if e.silkSideEncoder != nil {
		e.silkSideEncoder.Reset()
	}
	if e.celtEncoder != nil {
		e.celtEncoder.Reset()
	}

	// Reset SILK frame buffers

	// Reset FEC state
	e.resetFECState()

	// Reset DTX state
	if e.dtx != nil {
		e.dtx.reset()
	}
}

// SetFEC enables or disables in-band Forward Error Correction.
// When enabled, the encoder includes LBRR data for loss recovery.
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
// This affects FEC behavior and bitrate allocation.
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
// When enabled, packets are suppressed during silence.
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
// Higher values use more CPU for better quality.
// Default is 10 (maximum quality).
//
// Guidelines:
//
//	0-1: Minimal processing, fastest encoding
//	2-4: Basic analysis, good for real-time with limited CPU
//	5-7: Moderate analysis, balanced quality/speed
//	8-10: Thorough analysis, highest quality
func (e *Encoder) SetComplexity(complexity int) {
	if complexity < 0 {
		complexity = 0
	}
	if complexity > 10 {
		complexity = 10
	}
	e.complexity = complexity

	// Apply complexity to sub-encoders
	// For v1, this affects decision thresholds only
	// Future: affect MDCT precision, pitch search resolution, etc.
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
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after Encode() to get a meaningful value.
func (e *Encoder) FinalRange() uint32 {
	// Return the final range from the CELT encoder if it exists
	// (CELT is used for CELT-only and Hybrid modes)
	if e.celtEncoder != nil {
		return e.celtEncoder.FinalRange()
	}
	// SILK encoder final range for SILK-only mode
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
// Valid range is 6000-510000 (6 kbps to 510 kbps).
// Values outside this range are clamped.
func (e *Encoder) SetBitrate(bitrate int) {
	e.bitrate = ClampBitrate(bitrate)
}

// Bitrate returns the current target bitrate.
func (e *Encoder) Bitrate() int {
	return e.bitrate
}

// computePacketSize determines target packet size based on mode.
func (e *Encoder) computePacketSize(frameSize int) int {
	target := targetBytesForBitrate(e.bitrate, frameSize)

	switch e.bitrateMode {
	case ModeVBR:
		// No size constraint
		return 0 // 0 means unlimited

	case ModeCVBR:
		// Return target as hint; actual size can vary by CVBRTolerance
		return target

	case ModeCBR:
		// Return exact target
		return target
	}
	return 0
}

// Encode encodes PCM samples to an Opus frame.
// pcm: input samples as float64 (interleaved if stereo)
// frameSize: number of samples per channel (must match configured frame size)
//
// Returns the encoded Opus packet with TOC byte (complete packet ready for transmission).
// Returns nil, nil if DTX suppresses the frame (silence detected).
//
// For hybrid mode, SILK encodes first (0-8kHz), then CELT encodes second (8-20kHz),
// both using a shared range encoder per RFC 6716 Section 3.2.1.
func (e *Encoder) Encode(pcm []float64, frameSize int) ([]byte, error) {
	// Validate input length
	expectedLen := frameSize * e.channels
	if len(pcm) != expectedLen {
		return nil, ErrInvalidFrameSize
	}

	// Check DTX mode - suppress frames during silence
	suppressFrame, sendComfortNoise := e.shouldUseDTX(pcm)
	if suppressFrame {
		if sendComfortNoise {
			return e.encodeComfortNoise(frameSize)
		}
		// Return nil to indicate frame suppression
		return nil, nil
	}

	// Determine actual mode to use
	signalHint := e.signalType
	if e.mode == ModeAuto && e.signalType == types.SignalAuto {
		signalHint = e.autoSignalFromPCM(pcm, frameSize)
	}
	actualMode := e.selectMode(frameSize, signalHint)

	// Route to appropriate encoder (returns raw frame data without TOC)
	var frameData []byte
	var err error
	switch actualMode {
	case ModeSILK:
		frameData, err = e.encodeSILKFrame(pcm, frameSize)
	case ModeHybrid:
		frameData, err = e.encodeHybridFrame(pcm, frameSize)
	case ModeCELT:
		frameData, err = e.encodeCELTFrame(pcm, frameSize)
	default:
		return nil, ErrEncodingFailed
	}

	if err != nil {
		return nil, err
	}

	// Build complete packet with TOC byte into scratch buffer
	stereo := e.channels == 2
	packetLen, err := BuildPacketInto(e.scratchPacket, frameData, modeToTypes(actualMode), e.effectiveBandwidth(), frameSize, stereo)
	if err != nil {
		return nil, err
	}
	packet := e.scratchPacket[:packetLen]

	// Apply bitrate mode constraints
	switch e.bitrateMode {
	case ModeCVBR:
		target := targetBytesForBitrate(e.bitrate, frameSize)
		packet = constrainSize(packet, target, CVBRTolerance)
	case ModeCBR:
		target := targetBytesForBitrate(e.bitrate, frameSize)
		packet = padToSize(packet, target)
	}
	// ModeVBR: no constraint applied

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
		return types.ModeCELT // ModeAuto already resolved
	}
}

// selectMode determines the actual encoding mode based on settings and content.
func (e *Encoder) selectMode(frameSize int, signalHint types.Signal) Mode {
	// If mode is explicitly set (not auto), use it
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

	// Apply signal type hint to influence mode selection
	// SignalVoice biases toward SILK, SignalMusic toward CELT
	switch signalHint {
	case types.SignalVoice:
		// Voice signal: prefer SILK for lower bandwidths, Hybrid for higher
		switch bw {
		case types.BandwidthNarrowband, types.BandwidthMediumband, types.BandwidthWideband:
			return ModeSILK
		case types.BandwidthSuperwideband, types.BandwidthFullband:
			// Use Hybrid for voice at high bandwidth (if frame size supports it)
			if frameSize == 480 || frameSize == 960 {
				return ModeHybrid
			}
			return ModeSILK
		}
	case types.SignalMusic:
		// Music signal: prefer CELT for full-bandwidth audio
		return ModeCELT
	}

	// Auto mode selection based on bandwidth and frame size
	switch bw {
	case types.BandwidthNarrowband, types.BandwidthMediumband, types.BandwidthWideband:
		// Lower bandwidths: use SILK
		return ModeSILK
	case types.BandwidthSuperwideband:
		// Superwideband: use Hybrid for speech (10ms or 20ms frames)
		// Hybrid combines SILK (0-8kHz) with CELT (8-12kHz) for good speech quality
		// Now supports both mono and stereo
		if frameSize == 480 || frameSize == 960 {
			return ModeHybrid
		}
		return ModeCELT
	case types.BandwidthFullband:
		// Fullband: use CELT for best audio quality
		// CELT handles the full 0-20kHz range natively, no need for Hybrid
		return ModeCELT
	default:
		return ModeCELT
	}
}

// autoSignalFromPCM estimates signal type for ModeAuto when no hint is provided.
// Uses energy-based silence detection plus a simple high-frequency proxy.
func (e *Encoder) autoSignalFromPCM(pcm []float64, frameSize int) types.Signal {
	if len(pcm) == 0 || frameSize <= 0 {
		return types.SignalAuto
	}

	// Use existing energy-based classifier for silence gating.
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}
	signalType, _ := classifySignal(pcm32)
	if signalType == 0 {
		// Silence: bias toward SILK/DTX.
		return types.SignalVoice
	}

	// Compute a simple high-frequency proxy using first-difference energy.
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

	// Higher ratio implies more high-frequency content (music/percussive).
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
// Uses pre-allocated scratch buffers for zero allocations in hot path.
func (e *Encoder) encodeSILKFrame(pcm []float64, frameSize int) ([]byte, error) {
	// Ensure SILK encoder exists
	e.ensureSILKEncoder()

	// Convert to float32 for SILK using pre-allocated scratch buffer
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}

	// Downsample 48kHz -> SILK bandwidth sample rate for SILK-only mode
	// libopus feeds SILK at its native rate (8/12/16 kHz).
	cfg := silk.GetBandwidthConfig(e.silkBandwidth())
	targetRate := cfg.SampleRate
	if targetRate != 48000 {
		e.ensureSILKResampler(targetRate)
	}
	targetSamples := frameSize * targetRate / 48000
	if targetSamples <= 0 {
		targetSamples = len(pcm32)
	}

	// For stereo, need to handle separately
	if e.channels == 2 {
		perChannelRate := e.bitrate / e.channels
		if perChannelRate > 0 {
			e.silkEncoder.SetBitrate(perChannelRate)
		}
		e.silkEncoder.SetFEC(e.fecEnabled)
		e.silkEncoder.SetPacketLoss(e.packetLoss)
		// Ensure side encoder exists for stereo
		e.ensureSILKSideEncoder()
		if perChannelRate > 0 {
			e.silkSideEncoder.SetBitrate(perChannelRate)
		}
		e.silkSideEncoder.SetFEC(e.fecEnabled)
		e.silkSideEncoder.SetPacketLoss(e.packetLoss)
		// Deinterleave using pre-allocated scratch buffers
		left := e.scratchLeft[:frameSize]
		right := e.scratchRight[:frameSize]
		for i := 0; i < frameSize; i++ {
			left[i] = pcm32[i*2]
			right[i] = pcm32[i*2+1]
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
		// Compute VAD on mono mix at SILK sample rate
		fsKHz := targetRate / 1000
		mono := e.scratchMono[:len(left)]
		for i := 0; i < len(left); i++ {
			mono[i] = (left[i] + right[i]) * 0.5
		}
		vadFlag := e.computeSilkVAD(mono, len(left), fsKHz)
		e.silkEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.lastVADInputQualityQ15)
		e.silkSideEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.lastVADInputQualityQ15)

		return silk.EncodeStereoWithEncoder(e.silkEncoder, e.silkSideEncoder, left, right, e.silkBandwidth(), vadFlag)
	}

	if targetRate != 48000 {
		out := e.ensureSilkResampled(targetSamples)
		n := e.silkResampler.ProcessInto(pcm32, out)
		if n < len(out) {
			out = out[:n]
		}
		pcm32 = out
	}

	if e.bitrate > 0 {
		perChannelRate := e.bitrate / e.channels
		if perChannelRate > 0 {
			e.silkEncoder.SetBitrate(perChannelRate)
		}
	}
	e.silkEncoder.SetFEC(e.fecEnabled)
	e.silkEncoder.SetPacketLoss(e.packetLoss)

	// Compute VAD at SILK sample rate
	fsKHz := targetRate / 1000
	vadFlags, nFrames := e.computeSilkVADFlags(pcm32, fsKHz)
	e.silkEncoder.SetVADState(e.lastVADActivityQ8, e.lastVADInputTiltQ15, e.lastVADInputQualityQ15)

	// Mono encoding using persistent encoder
	if e.fecEnabled || nFrames > 1 {
		return e.silkEncoder.EncodePacketWithFEC(pcm32, vadFlags), nil
	}
	vadFlag := false
	if len(vadFlags) > 0 {
		vadFlag = vadFlags[0]
	}
	return silk.EncodeWithEncoder(e.silkEncoder, pcm32, e.silkBandwidth(), vadFlag)
}

// encodeCELTFrame encodes a frame using CELT-only mode.
func (e *Encoder) encodeCELTFrame(pcm []float64, frameSize int) ([]byte, error) {
	// Ensure CELT encoder exists
	e.ensureCELTEncoder()

	// Set bitrate for proper bit allocation
	e.celtEncoder.SetBitrate(e.bitrate)
	// Ensure CELT encoder is not in hybrid mode
	e.celtEncoder.SetHybrid(false)
	// Propagate packet loss for prefilter gain scaling
	e.celtEncoder.SetPacketLoss(e.packetLoss)
	// Propagate LSB depth to CELT for masking/spread decisions
	e.celtEncoder.SetLSBDepth(e.lsbDepth)

	// Propagate bitrate mode to CELT encoder
	// CBR mode: VBR=false, CVBR=false - encoder uses exact bit budget
	// CVBR mode: VBR=true, CVBR=true - encoder allows variation within constraints
	// VBR mode: VBR=true, CVBR=false - encoder freely varies bitrate
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

	// Use our encoder instance for stateful encoding with bitrate
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
		// Use DownsamplingResampler with proper AR2+FIR algorithm for encoder mode
		// This fixes the critical bug where IIR_FIR upsampling was used for downsampling
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
	e.lastVADInputQualityQ15 = (e.silkVAD.InputQualityBandsQ15[0] + e.silkVAD.InputQualityBandsQ15[1]) / 2
	e.lastVADActive = active
	e.lastVADValid = true
	return active
}

func (e *Encoder) computeSilkVADSide(mono []float32, frameSamples, fsKHz int) bool {
	if frameSamples <= 0 || fsKHz <= 0 {
		return false
	}
	e.ensureSilkVADSide()
	_, active := computeSilkVADWithState(e.silkVADSide, mono, frameSamples, fsKHz)
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
		// Hybrid mode uses WB for SILK layer
		return silk.BandwidthWideband
	default:
		return silk.BandwidthWideband
	}
}

// ValidFrameSize returns true if the frame size is valid for the given mode.
func ValidFrameSize(frameSize int, mode Mode) bool {
	switch mode {
	case ModeSILK:
		// SILK: 10, 20, 40, 60ms (480, 960, 1920, 2880 at 48kHz)
		return frameSize == 480 || frameSize == 960 || frameSize == 1920 || frameSize == 2880
	case ModeHybrid:
		// Hybrid: only 10, 20ms
		return frameSize == 480 || frameSize == 960
	case ModeCELT:
		// CELT: 2.5, 5, 10, 20ms (120, 240, 480, 960 at 48kHz)
		return frameSize == 120 || frameSize == 240 || frameSize == 480 || frameSize == 960
	default:
		// ModeAuto: accept all valid sizes
		return frameSize == 120 || frameSize == 240 || frameSize == 480 ||
			frameSize == 960 || frameSize == 1920 || frameSize == 2880
	}
}

// SetSignalType sets the signal type hint for mode selection.
// SignalVoice biases toward SILK mode, SignalMusic toward CELT mode.
func (e *Encoder) SetSignalType(signal types.Signal) {
	e.signalType = signal
}

// SignalType returns the current signal type hint.
func (e *Encoder) SignalType() types.Signal {
	return e.signalType
}

// SetMaxBandwidth sets the maximum bandwidth limit.
// The actual bandwidth will be clamped to this limit.
func (e *Encoder) SetMaxBandwidth(bw types.Bandwidth) {
	e.maxBandwidth = bw
}

// MaxBandwidth returns the maximum bandwidth limit.
func (e *Encoder) MaxBandwidth() types.Bandwidth {
	return e.maxBandwidth
}

// SetForceChannels sets the forced channel count.
// -1 = auto (use input channels), 1 = force mono, 2 = force stereo.
func (e *Encoder) SetForceChannels(channels int) {
	e.forceChannels = channels
}

// ForceChannels returns the forced channel count (-1 = auto).
func (e *Encoder) ForceChannels() int {
	return e.forceChannels
}

// Lookahead returns the encoder's algorithmic delay in samples at 48kHz.
// This includes both CELT delay compensation and mode-specific delay.
// Reference: libopus OPUS_GET_LOOKAHEAD
func (e *Encoder) Lookahead() int {
	// Base lookahead is sampleRate/400 (2.5ms) = 120 samples at 48kHz
	// Plus delay compensation: 130 samples for CELT overlap
	// Total: approximately 250 samples (5.2ms) at 48kHz
	baseLookahead := e.sampleRate / 400 // 2.5ms
	delayComp := 130                    // CELT delay compensation in 48kHz samples
	return baseLookahead + delayComp
}

// SetLSBDepth sets the input signal's LSB depth (8-24 bits).
// This affects DTX sensitivity - lower depths mean louder silence threshold.
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
// When true, each frame can be decoded independently, improving error resilience.
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	e.predictionDisabled = disabled
}

// PredictionDisabled returns whether inter-frame prediction is disabled.
func (e *Encoder) PredictionDisabled() bool {
	return e.predictionDisabled
}

// SetPhaseInversionDisabled disables stereo phase inversion.
// Phase inversion improves stereo decorrelation but may cause issues with some audio.
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	e.phaseInversionDisabled = disabled
	// Propagate to CELT encoder if it exists
	if e.celtEncoder != nil {
		e.celtEncoder.SetPhaseInversionDisabled(disabled)
	}
}

// PhaseInversionDisabled returns whether stereo phase inversion is disabled.
func (e *Encoder) PhaseInversionDisabled() bool {
	return e.phaseInversionDisabled
}
