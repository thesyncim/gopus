// Package encoder implements the unified Opus encoder per RFC 6716.
// It orchestrates SILK and CELT sub-encoders for hybrid mode encoding,
// which combines SILK (0-8kHz) with CELT (8-20kHz) for super-wideband
// and fullband speech encoding.
//
// Reference: RFC 6716 Section 3.2
package encoder

import (
	"errors"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/internal/types"
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
	silkEncoder *silk.Encoder
	celtEncoder *celt.Encoder

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
	fecEnabled bool
	packetLoss int // Expected packet loss percentage (0-100)
	fec        *fecState

	// DTX (Discontinuous Transmission) controls
	dtxEnabled bool
	dtx        *dtxState
	rng        uint32 // RNG for comfort noise

	// Complexity control (0-10, higher = better quality but slower)
	complexity int

	// Encoder state for CELT delay compensation
	// The 2.7ms delay (130 samples at 48kHz) aligns SILK and CELT
	prevSamples []float64
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

	return &Encoder{
		mode:        ModeAuto,
		bandwidth:   types.BandwidthFullband,
		sampleRate:  sampleRate,
		channels:    channels,
		frameSize:   960,       // Default 20ms
		bitrateMode: ModeVBR,   // VBR is default
		bitrate:     64000,     // 64 kbps default
		fecEnabled:  false,     // FEC disabled by default
		packetLoss:  0,         // 0% packet loss expected
		fec:         newFECState(),
		dtxEnabled:  false,
		dtx:         newDTXState(),
		rng:         22222,     // Match libopus seed
		complexity:  10,        // Default: highest quality
		prevSamples: make([]float64, 130*channels), // CELT delay compensation buffer
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
	if e.celtEncoder != nil {
		e.celtEncoder.Reset()
	}

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
}

// Complexity returns the current complexity setting.
func (e *Encoder) Complexity() int {
	return e.complexity
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
	actualMode := e.selectMode(frameSize)

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

	// Build complete packet with TOC byte
	stereo := e.channels == 2
	packet, err := BuildPacket(frameData, modeToTypes(actualMode), e.bandwidth, frameSize, stereo)
	if err != nil {
		return nil, err
	}

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
func (e *Encoder) selectMode(frameSize int) Mode {
	// If mode is explicitly set (not auto), use it
	if e.mode != ModeAuto {
		return e.mode
	}

	// Auto mode selection based on bandwidth and frame size
	switch e.bandwidth {
	case types.BandwidthNarrowband, types.BandwidthMediumband, types.BandwidthWideband:
		// Lower bandwidths: use SILK
		return ModeSILK
	case types.BandwidthSuperwideband, types.BandwidthFullband:
		// Higher bandwidths: use Hybrid for speech-like frames
		// Only if frame size is compatible with hybrid (10ms or 20ms)
		if frameSize == 480 || frameSize == 960 {
			return ModeHybrid
		}
		// Otherwise use CELT
		return ModeCELT
	default:
		return ModeCELT
	}
}

// encodeSILKFrame encodes a frame using SILK-only mode.
func (e *Encoder) encodeSILKFrame(pcm []float64, frameSize int) ([]byte, error) {
	// Ensure SILK encoder exists
	e.ensureSILKEncoder()

	// Convert to float32 for SILK
	pcm32 := make([]float32, len(pcm))
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}

	// For stereo, need to handle separately
	if e.channels == 2 {
		// Deinterleave
		left := make([]float32, frameSize)
		right := make([]float32, frameSize)
		for i := 0; i < frameSize; i++ {
			left[i] = pcm32[i*2]
			right[i] = pcm32[i*2+1]
		}
		return silk.EncodeStereo(left, right, e.silkBandwidth(), true)
	}

	// Mono encoding
	return silk.Encode(pcm32, e.silkBandwidth(), true)
}

// encodeCELTFrame encodes a frame using CELT-only mode.
func (e *Encoder) encodeCELTFrame(pcm []float64, frameSize int) ([]byte, error) {
	// Ensure CELT encoder exists
	e.ensureCELTEncoder()

	if e.channels == 2 {
		return celt.EncodeStereo(pcm, frameSize)
	}
	return celt.Encode(pcm, frameSize)
}

// ensureSILKEncoder creates the SILK encoder if it doesn't exist.
func (e *Encoder) ensureSILKEncoder() {
	if e.silkEncoder == nil {
		e.silkEncoder = silk.NewEncoder(e.silkBandwidth())
	}
}

// ensureCELTEncoder creates the CELT encoder if it doesn't exist.
func (e *Encoder) ensureCELTEncoder() {
	if e.celtEncoder == nil {
		e.celtEncoder = celt.NewEncoder(e.channels)
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

