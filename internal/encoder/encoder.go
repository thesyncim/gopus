// Package encoder implements the unified Opus encoder per RFC 6716.
// It orchestrates SILK and CELT sub-encoders for hybrid mode encoding,
// which combines SILK (0-8kHz) with CELT (8-20kHz) for super-wideband
// and fullband speech encoding.
//
// Reference: RFC 6716 Section 3.2
package encoder

import (
	"errors"

	"gopus"
	"gopus/internal/celt"
	"gopus/internal/silk"
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
	bandwidth  gopus.Bandwidth
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
		bandwidth:   gopus.BandwidthFullband,
		sampleRate:  sampleRate,
		channels:    channels,
		frameSize:   960, // Default 20ms
		bitrateMode: ModeVBR,  // VBR is default
		bitrate:     64000,    // 64 kbps default
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
func (e *Encoder) SetBandwidth(bandwidth gopus.Bandwidth) {
	e.bandwidth = bandwidth
}

// Bandwidth returns the current bandwidth setting.
func (e *Encoder) Bandwidth() gopus.Bandwidth {
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
}

// Encode encodes PCM samples to an Opus frame.
// pcm: input samples as float64 (interleaved if stereo)
// frameSize: number of samples per channel (must match configured frame size)
//
// Returns the encoded Opus frame data (without TOC byte - that's added in Plan 2).
//
// For hybrid mode, SILK encodes first (0-8kHz), then CELT encodes second (8-20kHz),
// both using a shared range encoder per RFC 6716 Section 3.2.1.
func (e *Encoder) Encode(pcm []float64, frameSize int) ([]byte, error) {
	// Validate input length
	expectedLen := frameSize * e.channels
	if len(pcm) != expectedLen {
		return nil, ErrInvalidFrameSize
	}

	// Determine actual mode to use
	actualMode := e.selectMode(frameSize)

	// Route to appropriate encoder
	switch actualMode {
	case ModeSILK:
		return e.encodeSILKFrame(pcm, frameSize)
	case ModeHybrid:
		return e.encodeHybridFrame(pcm, frameSize)
	case ModeCELT:
		return e.encodeCELTFrame(pcm, frameSize)
	default:
		return nil, ErrEncodingFailed
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
	case gopus.BandwidthNarrowband, gopus.BandwidthMediumband, gopus.BandwidthWideband:
		// Lower bandwidths: use SILK
		return ModeSILK
	case gopus.BandwidthSuperwideband, gopus.BandwidthFullband:
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
	case gopus.BandwidthNarrowband:
		return silk.BandwidthNarrowband
	case gopus.BandwidthMediumband:
		return silk.BandwidthMediumband
	case gopus.BandwidthWideband:
		return silk.BandwidthWideband
	case gopus.BandwidthSuperwideband, gopus.BandwidthFullband:
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
