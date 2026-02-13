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
	enc         *encoder.Encoder
	sampleRate  int
	channels    int
	frameSize   int
	application Application
	encodedOnce bool

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

	// Max frame size is 2880 samples (60ms at 48kHz) per channel
	maxSamples := 2880 * channels

	enc := &Encoder{
		enc:          encoder.NewEncoder(sampleRate, channels),
		sampleRate:   sampleRate,
		channels:     channels,
		frameSize:    960, // Default 20ms at 48kHz
		application:  application,
		scratchPCM64: make([]float64, maxSamples),
		scratchPCM32: make([]float32, maxSamples),
	}

	// Apply application hint
	enc.applyApplication(application)

	return enc, nil
}

// SetApplication updates the encoder application hint.
//
// Valid values are ApplicationVoIP, ApplicationAudio, and ApplicationLowDelay.
func (e *Encoder) SetApplication(application Application) error {
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
		e.enc.SetMode(encoder.ModeAuto)
		e.enc.SetBandwidth(types.BandwidthWideband) // 16kHz max
	case ApplicationAudio:
		// Prefer CELT/Hybrid for music
		e.enc.SetLowDelay(false)
		e.enc.SetMode(encoder.ModeAuto)
		e.enc.SetBandwidth(types.BandwidthFullband) // 48kHz
	case ApplicationLowDelay:
		// CELT only with small frames
		e.enc.SetLowDelay(true)
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
	// Convert int16 to float32 using pre-allocated scratch buffer (zero allocs)
	pcm32 := e.scratchPCM32[:len(pcm)]
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
// Disabling VBR switches to CBR. Enabling VBR switches to unconstrained VBR.
func (e *Encoder) SetVBR(enabled bool) {
	if enabled {
		e.enc.SetBitrateMode(encoder.ModeVBR)
		return
	}
	e.enc.SetBitrateMode(encoder.ModeCBR)
}

// VBR returns whether VBR mode is enabled.
func (e *Encoder) VBR() bool {
	return e.enc.GetBitrateMode() != encoder.ModeCBR
}

// SetVBRConstraint enables or disables VBR constraint.
//
// Enabling constraint switches to CVBR. Disabling constraint switches to VBR.
func (e *Encoder) SetVBRConstraint(constrained bool) {
	if constrained {
		e.enc.SetBitrateMode(encoder.ModeCVBR)
		return
	}
	e.enc.SetBitrateMode(encoder.ModeVBR)
}

// VBRConstraint returns whether VBR constraint is enabled.
func (e *Encoder) VBRConstraint() bool {
	return e.enc.GetBitrateMode() == encoder.ModeCVBR
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
	e.encodedOnce = false
}

// Channels returns the number of audio channels (1 or 2).
func (e *Encoder) Channels() int {
	return e.channels
}

// SampleRate returns the sample rate in Hz.
func (e *Encoder) SampleRate() int {
	return e.sampleRate
}

// FinalRange returns the final range coder state after encoding.
// This matches libopus OPUS_GET_FINAL_RANGE and is used for bitstream verification.
// Must be called after Encode() to get a meaningful value.
func (e *Encoder) FinalRange() uint32 {
	return e.enc.FinalRange()
}

// SetSignal sets the signal type hint for mode selection.
//
// signal must be one of:
//   - SignalAuto: automatically detect signal type
//   - SignalVoice: optimize for speech (biases toward SILK)
//   - SignalMusic: optimize for music (biases toward CELT)
//
// Returns ErrInvalidSignal if the value is not valid.
func (e *Encoder) SetSignal(signal Signal) error {
	switch signal {
	case SignalAuto, SignalVoice, SignalMusic:
		e.enc.SetSignalType(signal)
		return nil
	default:
		return ErrInvalidSignal
	}
}

// Signal returns the current signal type hint.
func (e *Encoder) Signal() Signal {
	return e.enc.SignalType()
}

// SetBandwidth sets the target audio bandwidth.
func (e *Encoder) SetBandwidth(bandwidth Bandwidth) error {
	switch bandwidth {
	case BandwidthNarrowband, BandwidthMediumband, BandwidthWideband, BandwidthSuperwideband, BandwidthFullband:
		e.enc.SetBandwidth(bandwidth)
		return nil
	default:
		return ErrInvalidBandwidth
	}
}

// Bandwidth returns the currently configured target bandwidth.
func (e *Encoder) Bandwidth() Bandwidth {
	return e.enc.Bandwidth()
}

// SetMaxBandwidth sets the maximum audio bandwidth.
//
// The encoder will not use a bandwidth higher than this limit.
// This is useful for limiting bandwidth without changing the sample rate.
//
// Valid values are:
//   - BandwidthNarrowband (4kHz)
//   - BandwidthMediumband (6kHz)
//   - BandwidthWideband (8kHz)
//   - BandwidthSuperwideband (12kHz)
//   - BandwidthFullband (20kHz)
func (e *Encoder) SetMaxBandwidth(bandwidth Bandwidth) {
	e.enc.SetMaxBandwidth(bandwidth)
}

// MaxBandwidth returns the current maximum bandwidth limit.
func (e *Encoder) MaxBandwidth() Bandwidth {
	return e.enc.MaxBandwidth()
}

// SetForceChannels forces the encoder to use a specific channel count.
//
// channels must be one of:
//   - -1: automatic (use input channels)
//   - 1: force mono output
//   - 2: force stereo output
//
// Note: Forcing stereo on mono input will duplicate the channel.
// Forcing mono on stereo input will downmix to mono.
//
// Returns ErrInvalidForceChannels if the value is not valid.
func (e *Encoder) SetForceChannels(channels int) error {
	if channels != -1 && channels != 1 && channels != 2 {
		return ErrInvalidForceChannels
	}
	e.enc.SetForceChannels(channels)
	return nil
}

// ForceChannels returns the forced channel count (-1 = auto).
func (e *Encoder) ForceChannels() int {
	return e.enc.ForceChannels()
}

// Lookahead returns the encoder's algorithmic delay in samples.
//
// Matches libopus OPUS_GET_LOOKAHEAD behavior:
//   - Base lookahead is Fs/400 (2.5ms)
//   - Delay compensation Fs/250 is included for VoIP/Audio
//   - Delay compensation is omitted for LowDelay
func (e *Encoder) Lookahead() int {
	base := e.sampleRate / 400
	if e.application == ApplicationLowDelay {
		return base
	}
	return base + e.sampleRate/250
}

// SetLSBDepth sets the bit depth of the input signal.
//
// depth must be 8-24. This affects DTX silence detection:
// lower bit depths have a higher noise floor, so the encoder
// adjusts its silence threshold accordingly.
//
// Default is 24 (full precision).
// Returns ErrInvalidLSBDepth if out of range.
func (e *Encoder) SetLSBDepth(depth int) error {
	if depth < 8 || depth > 24 {
		return ErrInvalidLSBDepth
	}
	e.enc.SetLSBDepth(depth)
	return nil
}

// LSBDepth returns the current input bit depth setting.
func (e *Encoder) LSBDepth() int {
	return e.enc.LSBDepth()
}

// SetPredictionDisabled disables inter-frame prediction.
//
// When disabled (true), each frame can be decoded independently,
// which improves error resilience at the cost of compression efficiency.
// This is useful for applications with high packet loss.
//
// Default is false (prediction enabled).
func (e *Encoder) SetPredictionDisabled(disabled bool) {
	e.enc.SetPredictionDisabled(disabled)
}

// PredictionDisabled returns whether inter-frame prediction is disabled.
func (e *Encoder) PredictionDisabled() bool {
	return e.enc.PredictionDisabled()
}

// SetPhaseInversionDisabled disables stereo phase inversion.
//
// Phase inversion is a technique used to improve stereo decorrelation.
// Some audio processing pipelines may have issues with phase-inverted audio.
// Disabling it (true) ensures no phase inversion is applied.
//
// Default is false (phase inversion enabled).
func (e *Encoder) SetPhaseInversionDisabled(disabled bool) {
	e.enc.SetPhaseInversionDisabled(disabled)
}

// PhaseInversionDisabled returns whether stereo phase inversion is disabled.
func (e *Encoder) PhaseInversionDisabled() bool {
	return e.enc.PhaseInversionDisabled()
}
