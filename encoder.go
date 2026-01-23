// encoder.go implements the public Encoder API for Opus encoding.

package gopus

import (
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

// Application hints the encoder for optimization.
type Application int

const (
	// ApplicationVoIP optimizes for speech transmission with low latency.
	// Prefers SILK mode for speech frequencies.
	ApplicationVoIP Application = iota

	// ApplicationAudio optimizes for music and high-quality audio.
	// Prefers CELT/Hybrid mode for full-bandwidth audio.
	ApplicationAudio

	// ApplicationLowDelay minimizes algorithmic delay.
	// Uses CELT mode exclusively with small frame sizes.
	ApplicationLowDelay
)

// Encoder encodes PCM audio samples into Opus packets.
//
// An Encoder instance maintains internal state and is NOT safe for concurrent use.
// Each goroutine should create its own Encoder instance.
//
// The encoder supports three modes:
//   - SILK: optimized for speech at lower bitrates
//   - CELT: optimized for music and high-quality audio
//   - Hybrid: combines SILK and CELT for wideband speech
//
// The mode is automatically selected based on the Application hint and bandwidth settings.
type Encoder struct {
	enc         *encoder.Encoder
	sampleRate  int
	channels    int
	frameSize   int
	application Application
}

// NewEncoder creates a new Opus encoder.
//
// sampleRate must be one of: 8000, 12000, 16000, 24000, 48000.
// channels must be 1 (mono) or 2 (stereo).
// application hints the encoder for optimization.
//
// Returns an error if the parameters are invalid.
func NewEncoder(sampleRate, channels int, application Application) (*Encoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if channels < 1 || channels > 2 {
		return nil, ErrInvalidChannels
	}

	enc := &Encoder{
		enc:         encoder.NewEncoder(sampleRate, channels),
		sampleRate:  sampleRate,
		channels:    channels,
		frameSize:   960, // Default 20ms at 48kHz
		application: application,
	}

	// Apply application hint
	enc.applyApplication(application)

	return enc, nil
}

// applyApplication configures the encoder based on the application hint.
func (e *Encoder) applyApplication(app Application) {
	switch app {
	case ApplicationVoIP:
		// Prefer SILK for speech
		e.enc.SetMode(encoder.ModeAuto)
		e.enc.SetBandwidth(types.BandwidthWideband) // 16kHz max
	case ApplicationAudio:
		// Prefer CELT/Hybrid for music
		e.enc.SetMode(encoder.ModeAuto)
		e.enc.SetBandwidth(types.BandwidthFullband) // 48kHz
	case ApplicationLowDelay:
		// CELT only with small frames
		e.enc.SetMode(encoder.ModeCELT)
		e.enc.SetBandwidth(types.BandwidthFullband)
	}
}

// Encode encodes float32 PCM samples into an Opus packet.
//
// pcm: Input samples (interleaved if stereo). Length must be frameSize * channels.
// data: Output buffer for the encoded packet. Recommended size is 4000 bytes.
//
// Returns the number of bytes written to data, or an error.
// Returns 0 bytes written if DTX suppresses the frame (silence detected).
//
// Buffer sizing: 4000 bytes is sufficient for any Opus packet.
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error) {
	expected := e.frameSize * e.channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}

	// Convert float32 to float64 for internal encoder
	pcm64 := make([]float64, len(pcm))
	for i, v := range pcm {
		pcm64[i] = float64(v)
	}

	// Encode
	packet, err := e.enc.Encode(pcm64, e.frameSize)
	if err != nil {
		return 0, err
	}

	// DTX: nil packet means silence suppressed
	if packet == nil {
		return 0, nil
	}

	if len(packet) > len(data) {
		return 0, ErrBufferTooSmall
	}

	copy(data, packet)
	return len(packet), nil
}

// EncodeInt16 encodes int16 PCM samples into an Opus packet.
//
// pcm: Input samples (interleaved if stereo). Length must be frameSize * channels.
// data: Output buffer for the encoded packet.
//
// Returns the number of bytes written to data, or an error.
//
// The samples are converted from int16 by dividing by 32768.
func (e *Encoder) EncodeInt16(pcm []int16, data []byte) (int, error) {
	// Convert int16 to float32
	pcm32 := make([]float32, len(pcm))
	for i, v := range pcm {
		pcm32[i] = float32(v) / 32768.0
	}
	return e.Encode(pcm32, data)
}

// EncodeFloat32 encodes float32 PCM samples and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use Encode with a pre-allocated buffer.
//
// pcm: Input samples (interleaved if stereo).
//
// Returns the encoded packet or an error.
func (e *Encoder) EncodeFloat32(pcm []float32) ([]byte, error) {
	// Allocate max packet size
	data := make([]byte, 4000)
	n, err := e.Encode(pcm, data)
	if err != nil {
		return nil, err
	}
	return data[:n], nil
}

// EncodeInt16Slice encodes int16 PCM samples and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use EncodeInt16 with a pre-allocated buffer.
//
// pcm: Input samples (interleaved if stereo).
//
// Returns the encoded packet or an error.
func (e *Encoder) EncodeInt16Slice(pcm []int16) ([]byte, error) {
	// Allocate max packet size
	data := make([]byte, 4000)
	n, err := e.EncodeInt16(pcm, data)
	if err != nil {
		return nil, err
	}
	return data[:n], nil
}

// SetBitrate sets the target bitrate in bits per second.
//
// Valid range is 6000 to 510000 (6 kbps to 510 kbps).
// Returns ErrInvalidBitrate if out of range.
func (e *Encoder) SetBitrate(bitrate int) error {
	if bitrate < 6000 || bitrate > 510000 {
		return ErrInvalidBitrate
	}
	e.enc.SetBitrate(bitrate)
	return nil
}

// Bitrate returns the current target bitrate in bits per second.
func (e *Encoder) Bitrate() int {
	return e.enc.Bitrate()
}

// SetComplexity sets the encoder's computational complexity.
//
// complexity must be 0-10, where:
//   - 0-1: Minimal processing, fastest encoding
//   - 2-4: Basic analysis, good for real-time with limited CPU
//   - 5-7: Moderate analysis, balanced quality/speed
//   - 8-10: Thorough analysis, highest quality
//
// Returns ErrInvalidComplexity if out of range.
func (e *Encoder) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return ErrInvalidComplexity
	}
	e.enc.SetComplexity(complexity)
	return nil
}

// Complexity returns the current complexity setting.
func (e *Encoder) Complexity() int {
	return e.enc.Complexity()
}

// SetFEC enables or disables in-band Forward Error Correction.
//
// When enabled, the encoder includes redundant information for loss recovery.
// FEC is most effective when the receiver can request retransmission of lost packets.
func (e *Encoder) SetFEC(enabled bool) {
	e.enc.SetFEC(enabled)
}

// FECEnabled returns whether FEC is enabled.
func (e *Encoder) FECEnabled() bool {
	return e.enc.FECEnabled()
}

// SetDTX enables or disables Discontinuous Transmission.
//
// When enabled, the encoder reduces bitrate during silence by:
//   - Suppressing packets entirely during silence
//   - Sending periodic comfort noise frames
//
// DTX is useful for VoIP applications to reduce bandwidth.
func (e *Encoder) SetDTX(enabled bool) {
	e.enc.SetDTX(enabled)
}

// DTXEnabled returns whether DTX is enabled.
func (e *Encoder) DTXEnabled() bool {
	return e.enc.DTXEnabled()
}

// SetFrameSize sets the frame size in samples at 48kHz.
//
// Valid sizes depend on the encoding mode:
//   - SILK: 480, 960, 1920, 2880 (10, 20, 40, 60 ms)
//   - CELT: 120, 240, 480, 960 (2.5, 5, 10, 20 ms)
//   - Hybrid: 480, 960 (10, 20 ms)
//
// Default is 960 (20ms).
func (e *Encoder) SetFrameSize(samples int) error {
	validSizes := map[int]bool{
		120:  true, // 2.5ms (CELT only)
		240:  true, // 5ms (CELT only)
		480:  true, // 10ms
		960:  true, // 20ms
		1920: true, // 40ms (SILK only)
		2880: true, // 60ms (SILK only)
	}
	if !validSizes[samples] {
		return ErrInvalidFrameSize
	}
	e.frameSize = samples
	e.enc.SetFrameSize(samples)
	return nil
}

// FrameSize returns the current frame size in samples at 48kHz.
func (e *Encoder) FrameSize() int {
	return e.frameSize
}

// Reset clears the encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *Encoder) Reset() {
	e.enc.Reset()
}

// Channels returns the number of audio channels (1 or 2).
func (e *Encoder) Channels() int {
	return e.channels
}

// SampleRate returns the sample rate in Hz.
func (e *Encoder) SampleRate() int {
	return e.sampleRate
}
