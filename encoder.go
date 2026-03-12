// encoder.go implements the public Encoder API for Opus encoding.

package gopus

import (
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
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

	// ApplicationRestrictedSilk forces SILK-only encoding.
	// This matches libopus OPUS_APPLICATION_RESTRICTED_SILK init-time behavior.
	ApplicationRestrictedSilk

	// ApplicationRestrictedCelt forces CELT-only encoding with low-delay semantics.
	// This matches libopus OPUS_APPLICATION_RESTRICTED_CELT init-time behavior.
	ApplicationRestrictedCelt
)

// Signal represents a hint about the input signal type.
// This helps the encoder optimize for speech or music content.
type Signal = types.Signal

const (
	// SignalAuto lets the encoder detect the signal type automatically.
	SignalAuto = types.SignalAuto
	// SignalVoice hints that the input is speech, biasing toward SILK mode.
	SignalVoice = types.SignalVoice
	// SignalMusic hints that the input is music, biasing toward CELT mode.
	SignalMusic = types.SignalMusic
)

// BitrateMode controls how the encoder sizes packets.
type BitrateMode = encoder.BitrateMode

const (
	// BitrateModeVBR enables unconstrained variable bitrate mode.
	BitrateModeVBR = encoder.ModeVBR
	// BitrateModeCVBR enables constrained variable bitrate mode.
	BitrateModeCVBR = encoder.ModeCVBR
	// BitrateModeCBR enables constant bitrate mode.
	BitrateModeCBR = encoder.ModeCBR
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
//
// Zero-allocation design: All scratch buffers are pre-allocated at construction time.
// The Encode and EncodeInt16 methods perform zero heap allocations in the hot path
// when called with properly sized caller-provided buffers.
type Encoder struct {
	enc                 *encoder.Encoder
	sampleRate          int
	channels            int
	frameSize           int
	expertFrameDuration ExpertFrameDuration
	application         Application
	encodedOnce         bool

	// Scratch buffers for zero-allocation encoding
	scratchPCM64 []float64 // float32 to float64 conversion buffer
	scratchPCM32 []float32 // int16 to float32 conversion buffer
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
	if !validApplication(application) {
		return nil, ErrInvalidApplication
	}

	// Max frame size is 5760 samples (120ms at 48kHz) per channel.
	maxSamples := 5760 * channels

	enc := &Encoder{
		enc:                 encoder.NewEncoder(sampleRate, channels),
		sampleRate:          sampleRate,
		channels:            channels,
		frameSize:           960, // Default 20ms at 48kHz
		expertFrameDuration: ExpertFrameDurationArg,
		application:         application,
		scratchPCM64:        make([]float64, maxSamples),
		scratchPCM32:        make([]float32, maxSamples),
	}

	// Apply application hint
	enc.applyApplication(application)

	return enc, nil
}

// SetApplication updates the encoder application hint.
//
// Valid values are ApplicationVoIP, ApplicationAudio, and ApplicationLowDelay.
func (e *Encoder) SetApplication(application Application) error {
	if e.application == ApplicationRestrictedSilk || e.application == ApplicationRestrictedCelt {
		return ErrInvalidApplication
	}
	switch application {
	case ApplicationVoIP, ApplicationAudio, ApplicationLowDelay:
		// Match libopus ctl semantics: after first successful encode call,
		// changing application is rejected (setting the same value remains valid).
		if e.encodedOnce && e.application != application {
			return ErrInvalidApplication
		}
		e.applyApplication(application)
		return nil
	default:
		return ErrInvalidApplication
	}
}

// Application returns the current encoder application hint.
func (e *Encoder) Application() Application {
	return e.application
}

// applyApplication configures the encoder based on the application hint.
func (e *Encoder) applyApplication(app Application) {
	e.application = app
	switch app {
	case ApplicationVoIP:
		// Prefer SILK for speech
		e.enc.SetLowDelay(false)
		e.enc.SetVoIPApplication(true)
		e.enc.SetMode(encoder.ModeAuto)
		e.enc.SetBandwidth(types.BandwidthWideband) // 16kHz max
		e.enc.SetSignalType(types.SignalAuto)
	case ApplicationAudio:
		// Prefer CELT/Hybrid for music
		e.enc.SetLowDelay(false)
		e.enc.SetVoIPApplication(false)
		e.enc.SetMode(encoder.ModeAuto)
		e.enc.SetBandwidth(types.BandwidthFullband) // 48kHz
		e.enc.SetSignalType(types.SignalAuto)
	case ApplicationLowDelay:
		// CELT only with small frames
		e.enc.SetLowDelay(true)
		e.enc.SetVoIPApplication(false)
		e.enc.SetMode(encoder.ModeCELT)
		e.enc.SetBandwidth(types.BandwidthFullband)
		e.enc.SetSignalType(types.SignalAuto)
	case ApplicationRestrictedSilk:
		// Experts-only SILK-only application.
		e.enc.SetLowDelay(false)
		e.enc.SetVoIPApplication(false)
		e.enc.SetMode(encoder.ModeSILK)
		e.enc.SetBandwidth(types.BandwidthWideband)
		e.enc.SetSignalType(types.SignalAuto)
	case ApplicationRestrictedCelt:
		// Experts-only CELT-only application with low-delay lookahead semantics.
		e.enc.SetLowDelay(true)
		e.enc.SetVoIPApplication(false)
		e.enc.SetMode(encoder.ModeCELT)
		e.enc.SetBandwidth(types.BandwidthFullband)
		e.enc.SetSignalType(types.SignalAuto)
	}
}

// Encode encodes float32 PCM samples into an Opus packet.
//
// pcm: Input samples (interleaved if stereo). Length must be frameSize * channels.
// data: Output buffer for the encoded packet. Recommended size is 4000 bytes.
//
// Returns the number of bytes written to data, or an error.
// When DTX is active during silence, returns a 1-byte TOC-only packet.
// Returns 0 bytes only when buffering (internal lookahead not yet filled).
//
// Buffer sizing: 4000 bytes is sufficient for any Opus packet.
func (e *Encoder) Encode(pcm []float32, data []byte) (int, error) {
	expected := e.frameSize * e.channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}
	e.enc.SetFloatInputFrame(pcm)
	defer e.enc.ClearFloatInputFrame()

	// Convert float32 to float64 using pre-allocated scratch buffer (zero allocs)
	pcm64 := e.scratchPCM64[:len(pcm)]
	for i, v := range pcm {
		pcm64[i] = float64(v)
	}

	// Encode
	packet, err := e.enc.Encode(pcm64, e.frameSize)
	if err != nil {
		return 0, err
	}
	e.encodedOnce = true

	// nil packet means internal buffering (lookahead not yet filled)
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
	// Convert int16 to float32 using pre-allocated scratch buffer (zero allocs)
	pcm32 := e.scratchPCM32[:len(pcm)]
	for i, v := range pcm {
		pcm32[i] = float32(v) / 32768.0
	}
	return e.Encode(pcm32, data)
}

// EncodeInt24 encodes 24-bit PCM samples stored in int32 values into an Opus packet.
//
// pcm: Input samples (interleaved if stereo). Length must be frameSize * channels.
// data: Output buffer for the encoded packet.
//
// Returns the number of bytes written to data, or an error.
//
// The input values are interpreted with the same semantics as libopus
// opus_encode24(): signed 24-bit PCM carried in int32 containers.
func (e *Encoder) EncodeInt24(pcm []int32, data []byte) (int, error) {
	expected := e.frameSize * e.channels
	if len(pcm) != expected {
		return 0, ErrInvalidFrameSize
	}

	// Convert 24-bit PCM stored in int32 containers to normalized float64.
	pcm64 := e.scratchPCM64[:len(pcm)]
	for i, v := range pcm {
		pcm64[i] = float64(v) / 8388608.0
	}

	packet, err := e.enc.Encode(pcm64, e.frameSize)
	if err != nil {
		return 0, err
	}
	e.encodedOnce = true

	if packet == nil {
		return 0, nil
	}

	if len(packet) > len(data) {
		return 0, ErrBufferTooSmall
	}

	copy(data, packet)
	return len(packet), nil
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

// EncodeInt24Slice encodes 24-bit PCM samples stored in int32 values and returns a new byte slice.
//
// This is a convenience method that allocates the output buffer.
// For performance-critical code, use EncodeInt24 with a pre-allocated buffer.
func (e *Encoder) EncodeInt24Slice(pcm []int32) ([]byte, error) {
	data := make([]byte, 4000)
	n, err := e.EncodeInt24(pcm, data)
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

// SetBitrateMode sets the encoder bitrate control mode.
func (e *Encoder) SetBitrateMode(mode BitrateMode) error {
	switch mode {
	case BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR:
		e.enc.SetBitrateMode(mode)
		return nil
	default:
		return ErrInvalidBitrateMode
	}
}

// BitrateMode returns the active encoder bitrate control mode.
func (e *Encoder) BitrateMode() BitrateMode {
	return e.enc.GetBitrateMode()
}

// SetVBR enables or disables VBR mode.
//
// Disabling VBR switches to CBR. Enabling VBR restores VBR while preserving
// the current VBR constraint state.
func (e *Encoder) SetVBR(enabled bool) {
	e.enc.SetVBR(enabled)
}

// VBR returns whether VBR mode is enabled.
func (e *Encoder) VBR() bool {
	return e.enc.VBR()
}

// SetVBRConstraint enables or disables VBR constraint.
//
// This setting is remembered even while VBR is disabled.
func (e *Encoder) SetVBRConstraint(constrained bool) {
	e.enc.SetVBRConstraint(constrained)
}

// VBRConstraint returns whether VBR constraint is enabled.
func (e *Encoder) VBRConstraint() bool {
	return e.enc.VBRConstraint()
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

// SetPacketLoss sets the expected packet loss percentage.
//
// lossPercent must be in the range [0, 100].
func (e *Encoder) SetPacketLoss(lossPercent int) error {
	if lossPercent < 0 || lossPercent > 100 {
		return ErrInvalidPacketLoss
	}
	e.enc.SetPacketLoss(lossPercent)
	return nil
}

// PacketLoss returns the configured expected packet loss percentage.
func (e *Encoder) PacketLoss() int {
	return e.enc.PacketLoss()
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
