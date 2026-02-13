// multistream.go implements the public Multistream API for Opus surround sound encoding and decoding.

package gopus

import (
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/multistream"
	"github.com/thesyncim/gopus/types"
)

// MultistreamEncoder encodes multi-channel PCM audio into Opus multistream packets.
//
// A MultistreamEncoder instance maintains internal state and is NOT safe for concurrent use.
// Each goroutine should create its own MultistreamEncoder instance.
//
// Multistream encoding is used for surround sound configurations (5.1, 7.1, etc.)
// where multiple coupled (stereo) and uncoupled (mono) streams are combined.
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1
type MultistreamEncoder struct {
	enc         *multistream.Encoder
	sampleRate  int
	channels    int
	frameSize   int
	application Application
	encodedOnce bool
}

// NewMultistreamEncoder creates a new multistream encoder with explicit configuration.
//
// Parameters:
//   - sampleRate: input sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total input channels (1-255)
//   - streams: total elementary streams (N, 1-255)
//   - coupledStreams: number of coupled stereo streams (M, 0 to streams)
//   - mapping: channel mapping table (length must equal channels)
//   - application: application hint for encoder optimization
//
// The mapping table determines how input audio is routed to stream encoders:
//   - Values 0 to 2*M-1: to coupled streams (even=left, odd=right of stereo pair)
//   - Values 2*M to N+M-1: to uncoupled (mono) streams
//   - Value 255: silent channel (input ignored)
//
// Example for 5.1 surround (6 channels, 4 streams, 2 coupled):
//
//	mapping = [0, 4, 1, 2, 3, 5]
//	  Input 0 (FL): mapping[0]=0 -> coupled stream 0, left
//	  Input 1 (C):  mapping[1]=4 -> uncoupled stream 2 (2*2+0)
//	  Input 2 (FR): mapping[2]=1 -> coupled stream 0, right
//	  Input 3 (RL): mapping[3]=2 -> coupled stream 1, left
//	  Input 4 (RR): mapping[4]=3 -> coupled stream 1, right
//	  Input 5 (LFE): mapping[5]=5 -> uncoupled stream 3 (2*2+1)
func NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams int, mapping []byte, application Application) (*MultistreamEncoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if channels < 1 || channels > 255 {
		return nil, ErrInvalidChannels
	}
	if streams < 1 || streams > 255 {
		return nil, ErrInvalidStreams
	}
	if coupledStreams < 0 || coupledStreams > streams {
		return nil, ErrInvalidCoupledStreams
	}
	if len(mapping) != channels {
		return nil, ErrInvalidMapping
	}

	enc, err := multistream.NewEncoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		return nil, err
	}

	mse := &MultistreamEncoder{
		enc:         enc,
		sampleRate:  sampleRate,
		channels:    channels,
		frameSize:   960, // Default 20ms at 48kHz
		application: application,
	}

	// Apply application hint
	mse.applyApplication(application)

	return mse, nil
}

// NewMultistreamEncoderDefault creates a multistream encoder with default Vorbis-style mapping
// for standard channel configurations (1-8 channels).
//
// This is a convenience function that calls the internal DefaultMapping() to get the appropriate
// streams, coupledStreams, and mapping for the given channel count.
//
// Supported channel counts:
//   - 1: mono (1 stream, 0 coupled)
//   - 2: stereo (1 stream, 1 coupled)
//   - 3: 3.0 (2 streams, 1 coupled)
//   - 4: quad (2 streams, 2 coupled)
//   - 5: 5.0 (3 streams, 2 coupled)
//   - 6: 5.1 surround (4 streams, 2 coupled)
//   - 7: 6.1 surround (5 streams, 2 coupled)
//   - 8: 7.1 surround (5 streams, 3 coupled)
func NewMultistreamEncoderDefault(sampleRate, channels int, application Application) (*MultistreamEncoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if channels < 1 || channels > 8 {
		return nil, ErrInvalidChannels
	}

	enc, err := multistream.NewEncoderDefault(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	mse := &MultistreamEncoder{
		enc:         enc,
		sampleRate:  sampleRate,
		channels:    channels,
		frameSize:   960, // Default 20ms at 48kHz
		application: application,
	}

	// Apply application hints
	mse.applyApplication(application)

	return mse, nil
}

// applyApplication records the application hint and forwards per-stream policy.
//
// Match libopus multistream OPUS_SET_APPLICATION forwarding semantics while
// preserving bitrate/complexity controls.
func (e *MultistreamEncoder) applyApplication(app Application) {
	e.application = app
	switch app {
	case ApplicationVoIP:
		e.enc.SetLowDelay(false)
		e.enc.SetMode(encoder.ModeAuto)
		e.enc.SetBandwidth(types.BandwidthWideband)
	case ApplicationAudio:
		e.enc.SetLowDelay(false)
		e.enc.SetMode(encoder.ModeAuto)
		e.enc.SetBandwidth(types.BandwidthFullband)
	case ApplicationLowDelay:
		e.enc.SetLowDelay(true)
		e.enc.SetMode(encoder.ModeCELT)
		e.enc.SetBandwidth(types.BandwidthFullband)
	}
}

// SetApplication updates the encoder application hint.
//
// Valid values are ApplicationVoIP, ApplicationAudio, and ApplicationLowDelay.
func (e *MultistreamEncoder) SetApplication(application Application) error {
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
func (e *MultistreamEncoder) Application() Application {
	return e.application
}

// Encode encodes float32 PCM samples into an Opus multistream packet.
//
// pcm: Input samples (interleaved). Length must be frameSize * channels.
// data: Output buffer for the encoded packet. Recommended size is 4000 bytes per stream.
//
// Returns the number of bytes written to data, or an error.
// Returns 0 bytes written if DTX suppresses all frames (silence detected in all streams).
func (e *MultistreamEncoder) Encode(pcm []float32, data []byte) (int, error) {
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
	e.encodedOnce = true

	// DTX: nil packet means all streams suppressed
	if packet == nil {
		return 0, nil
	}

	if len(packet) > len(data) {
		return 0, ErrBufferTooSmall
	}

	copy(data, packet)
	return len(packet), nil
}

// EncodeInt16 encodes int16 PCM samples into an Opus multistream packet.
//
// pcm: Input samples (interleaved). Length must be frameSize * channels.
// data: Output buffer for the encoded packet.
//
// Returns the number of bytes written to data, or an error.
func (e *MultistreamEncoder) EncodeInt16(pcm []int16, data []byte) (int, error) {
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
// pcm: Input samples (interleaved).
//
// Returns the encoded packet or an error.
func (e *MultistreamEncoder) EncodeFloat32(pcm []float32) ([]byte, error) {
	// Allocate max packet size (4000 bytes per stream is more than enough)
	data := make([]byte, 4000*e.enc.Streams())
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
// pcm: Input samples (interleaved).
//
// Returns the encoded packet or an error.
func (e *MultistreamEncoder) EncodeInt16Slice(pcm []int16) ([]byte, error) {
	// Allocate max packet size
	data := make([]byte, 4000*e.enc.Streams())
	n, err := e.EncodeInt16(pcm, data)
	if err != nil {
		return nil, err
	}
	return data[:n], nil
}

// SetBitrate sets the total target bitrate in bits per second.
//
// The bitrate is distributed across streams with coupled streams getting
// proportionally more bits than mono streams.
//
// Valid range is 6000 to 510000 per channel.
func (e *MultistreamEncoder) SetBitrate(bitrate int) error {
	if bitrate < 6000 || bitrate > 510000*e.channels {
		return ErrInvalidBitrate
	}
	e.enc.SetBitrate(bitrate)
	return nil
}

// Bitrate returns the current total target bitrate in bits per second.
func (e *MultistreamEncoder) Bitrate() int {
	return e.enc.Bitrate()
}

// SetComplexity sets the encoder's computational complexity for all streams.
//
// complexity must be 0-10, where:
//   - 0-1: Minimal processing, fastest encoding
//   - 2-4: Basic analysis, good for real-time with limited CPU
//   - 5-7: Moderate analysis, balanced quality/speed
//   - 8-10: Thorough analysis, highest quality
//
// Returns ErrInvalidComplexity if out of range.
func (e *MultistreamEncoder) SetComplexity(complexity int) error {
	if complexity < 0 || complexity > 10 {
		return ErrInvalidComplexity
	}
	e.enc.SetComplexity(complexity)
	return nil
}

// Complexity returns the current complexity setting.
func (e *MultistreamEncoder) Complexity() int {
	return e.enc.Complexity()
}

// SetBitrateMode sets the multistream encoder bitrate control mode.
func (e *MultistreamEncoder) SetBitrateMode(mode BitrateMode) error {
	switch mode {
	case BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR:
		e.enc.SetBitrateMode(mode)
		return nil
	default:
		return ErrInvalidBitrateMode
	}
}

// BitrateMode returns the active bitrate control mode.
func (e *MultistreamEncoder) BitrateMode() BitrateMode {
	return e.enc.BitrateMode()
}

// SetVBR enables or disables VBR mode.
//
// Disabling VBR switches to CBR. Enabling VBR restores VBR while preserving
// the current VBR constraint state.
func (e *MultistreamEncoder) SetVBR(enabled bool) {
	e.enc.SetVBR(enabled)
}

// VBR reports whether VBR mode is enabled.
func (e *MultistreamEncoder) VBR() bool {
	return e.enc.VBR()
}

// SetVBRConstraint enables or disables constrained VBR mode.
// This setting is remembered even while VBR is disabled.
func (e *MultistreamEncoder) SetVBRConstraint(constrained bool) {
	e.enc.SetVBRConstraint(constrained)
}

// VBRConstraint reports whether constrained VBR mode is enabled.
func (e *MultistreamEncoder) VBRConstraint() bool {
	return e.enc.VBRConstraint()
}

// SetFEC enables or disables in-band Forward Error Correction for all streams.
//
// When enabled, the encoders include redundant information for loss recovery.
func (e *MultistreamEncoder) SetFEC(enabled bool) {
	e.enc.SetFEC(enabled)
}

// FECEnabled returns whether FEC is enabled.
func (e *MultistreamEncoder) FECEnabled() bool {
	return e.enc.FECEnabled()
}

// SetDTX enables or disables Discontinuous Transmission for all streams.
//
// When enabled, the encoders reduce bitrate during silence by:
//   - Suppressing packets entirely during silence
//   - Sending periodic comfort noise frames
func (e *MultistreamEncoder) SetDTX(enabled bool) {
	e.enc.SetDTX(enabled)
}

// DTXEnabled returns whether DTX is enabled.
func (e *MultistreamEncoder) DTXEnabled() bool {
	return e.enc.DTXEnabled()
}

// SetPacketLoss sets expected packet loss percentage for all stream encoders.
//
// lossPercent must be in the range [0, 100].
func (e *MultistreamEncoder) SetPacketLoss(lossPercent int) error {
	if lossPercent < 0 || lossPercent > 100 {
		return ErrInvalidPacketLoss
	}
	e.enc.SetPacketLoss(lossPercent)
	return nil
}

// PacketLoss returns expected packet loss percentage.
func (e *MultistreamEncoder) PacketLoss() int {
	return e.enc.PacketLoss()
}

// SetBandwidth sets the target audio bandwidth.
func (e *MultistreamEncoder) SetBandwidth(bw Bandwidth) error {
	switch bw {
	case BandwidthNarrowband, BandwidthMediumband, BandwidthWideband, BandwidthSuperwideband, BandwidthFullband:
		e.enc.SetBandwidth(types.Bandwidth(bw))
		return nil
	default:
		return ErrInvalidBandwidth
	}
}

// Bandwidth returns the currently configured target bandwidth.
func (e *MultistreamEncoder) Bandwidth() Bandwidth {
	return Bandwidth(e.enc.Bandwidth())
}

// SetForceChannels forces channel count on all stream encoders.
//
// channels must be -1 (auto), 1 (mono), or 2 (stereo).
func (e *MultistreamEncoder) SetForceChannels(channels int) error {
	if channels != -1 && channels != 1 && channels != 2 {
		return ErrInvalidForceChannels
	}
	e.enc.SetForceChannels(channels)
	return nil
}

// ForceChannels returns the forced channel count (-1 = auto).
func (e *MultistreamEncoder) ForceChannels() int {
	return e.enc.ForceChannels()
}

// SetPredictionDisabled toggles inter-frame prediction on all stream encoders.
func (e *MultistreamEncoder) SetPredictionDisabled(disabled bool) {
	e.enc.SetPredictionDisabled(disabled)
}

// PredictionDisabled reports whether inter-frame prediction is disabled.
func (e *MultistreamEncoder) PredictionDisabled() bool {
	return e.enc.PredictionDisabled()
}

// SetPhaseInversionDisabled toggles stereo phase inversion on all stream encoders.
func (e *MultistreamEncoder) SetPhaseInversionDisabled(disabled bool) {
	e.enc.SetPhaseInversionDisabled(disabled)
}

// PhaseInversionDisabled reports whether stereo phase inversion is disabled.
func (e *MultistreamEncoder) PhaseInversionDisabled() bool {
	return e.enc.PhaseInversionDisabled()
}

// Reset clears the encoder state for a new stream.
// Call this when starting to encode a new audio stream.
func (e *MultistreamEncoder) Reset() {
	e.enc.Reset()
	e.encodedOnce = false
}

// Channels returns the number of audio channels.
func (e *MultistreamEncoder) Channels() int {
	return e.channels
}

// SampleRate returns the sample rate in Hz.
func (e *MultistreamEncoder) SampleRate() int {
	return e.sampleRate
}

// Streams returns the total number of elementary streams.
func (e *MultistreamEncoder) Streams() int {
	return e.enc.Streams()
}

// CoupledStreams returns the number of coupled (stereo) streams.
func (e *MultistreamEncoder) CoupledStreams() int {
	return e.enc.CoupledStreams()
}

// GetFinalRange returns the final range coder state for all streams.
// The values from all streams are XOR combined to produce a single verification value.
// This matches libopus OPUS_GET_FINAL_RANGE for multistream encoders.
// Must be called after Encode() to get a meaningful value.
func (e *MultistreamEncoder) GetFinalRange() uint32 {
	return e.enc.GetFinalRange()
}

// FinalRange returns the final range coder state.
func (e *MultistreamEncoder) FinalRange() uint32 {
	return e.GetFinalRange()
}

// Lookahead returns the encoder's algorithmic delay in samples.
//
// Matches libopus OPUS_GET_LOOKAHEAD behavior:
//   - Base lookahead is Fs/400 (2.5ms)
//   - Delay compensation Fs/250 is included for VoIP/Audio
//   - Delay compensation is omitted for LowDelay
func (e *MultistreamEncoder) Lookahead() int {
	base := e.sampleRate / 400
	if e.application == ApplicationLowDelay {
		return base
	}
	return base + e.sampleRate/250
}

// Signal returns the current signal type hint.
// Returns SignalAuto, SignalVoice, or SignalMusic.
func (e *MultistreamEncoder) Signal() Signal {
	return Signal(e.enc.Signal())
}

// SetSignal sets the signal type hint for all stream encoders.
// Use SignalVoice for speech content, SignalMusic for music content,
// or SignalAuto (default) for automatic detection.
func (e *MultistreamEncoder) SetSignal(signal Signal) error {
	switch signal {
	case SignalAuto, SignalVoice, SignalMusic:
		e.enc.SetSignal(types.Signal(signal))
		return nil
	default:
		return ErrInvalidSignal
	}
}

// SetMaxBandwidth sets the maximum bandwidth limit for all stream encoders.
// The actual bandwidth will be clamped to this limit.
func (e *MultistreamEncoder) SetMaxBandwidth(bw Bandwidth) error {
	switch bw {
	case BandwidthNarrowband, BandwidthMediumband, BandwidthWideband, BandwidthSuperwideband, BandwidthFullband:
		e.enc.SetMaxBandwidth(types.Bandwidth(bw))
		return nil
	default:
		return ErrInvalidBandwidth
	}
}

// MaxBandwidth returns the maximum bandwidth limit.
func (e *MultistreamEncoder) MaxBandwidth() Bandwidth {
	return Bandwidth(e.enc.MaxBandwidth())
}

// SetLSBDepth sets the input signal's LSB depth for all stream encoders.
// Valid range is 8-24 bits. This affects DTX sensitivity.
// Returns an error if the depth is out of range.
func (e *MultistreamEncoder) SetLSBDepth(depth int) error {
	return e.enc.SetLSBDepth(depth)
}

// LSBDepth returns the current LSB depth setting.
func (e *MultistreamEncoder) LSBDepth() int {
	return e.enc.LSBDepth()
}

// MultistreamDecoder decodes Opus multistream packets into multi-channel PCM audio.
//
// A MultistreamDecoder instance maintains internal state and is NOT safe for concurrent use.
// Each goroutine should create its own MultistreamDecoder instance.
//
// Multistream decoding is used for surround sound configurations (5.1, 7.1, etc.)
// where multiple coupled (stereo) and uncoupled (mono) streams are combined.
//
// Reference: RFC 6716 Appendix B, RFC 7845 Section 5.1.1
type MultistreamDecoder struct {
	dec           *multistream.Decoder
	sampleRate    int
	channels      int
	lastFrameSize int
}

// NewMultistreamDecoder creates a new multistream decoder with explicit configuration.
//
// Parameters:
//   - sampleRate: output sample rate (8000, 12000, 16000, 24000, or 48000 Hz)
//   - channels: total output channels (1-255)
//   - streams: total elementary streams (N, 1-255)
//   - coupledStreams: number of coupled stereo streams (M, 0 to streams)
//   - mapping: channel mapping table (length must equal channels)
//
// The mapping table determines how decoded audio is routed to output channels:
//   - Values 0 to 2*M-1: from coupled streams (even=left, odd=right of stereo pair)
//   - Values 2*M to N+M-1: from uncoupled (mono) streams
//   - Value 255: silent channel (output zeros)
func NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams int, mapping []byte) (*MultistreamDecoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if channels < 1 || channels > 255 {
		return nil, ErrInvalidChannels
	}
	if streams < 1 || streams > 255 {
		return nil, ErrInvalidStreams
	}
	if coupledStreams < 0 || coupledStreams > streams {
		return nil, ErrInvalidCoupledStreams
	}
	if len(mapping) != channels {
		return nil, ErrInvalidMapping
	}

	dec, err := multistream.NewDecoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		return nil, err
	}

	return &MultistreamDecoder{
		dec:           dec,
		sampleRate:    sampleRate,
		channels:      channels,
		lastFrameSize: 960, // Default 20ms at 48kHz
	}, nil
}

// NewMultistreamDecoderDefault creates a multistream decoder with default Vorbis-style mapping
// for standard channel configurations (1-8 channels).
//
// This is a convenience function that calls the internal DefaultMapping() to get the appropriate
// streams, coupledStreams, and mapping for the given channel count.
//
// Supported channel counts:
//   - 1: mono (1 stream, 0 coupled)
//   - 2: stereo (1 stream, 1 coupled)
//   - 3: 3.0 (2 streams, 1 coupled)
//   - 4: quad (2 streams, 2 coupled)
//   - 5: 5.0 (3 streams, 2 coupled)
//   - 6: 5.1 surround (4 streams, 2 coupled)
//   - 7: 6.1 surround (5 streams, 2 coupled)
//   - 8: 7.1 surround (5 streams, 3 coupled)
func NewMultistreamDecoderDefault(sampleRate, channels int) (*MultistreamDecoder, error) {
	if !validSampleRate(sampleRate) {
		return nil, ErrInvalidSampleRate
	}
	if channels < 1 || channels > 8 {
		return nil, ErrInvalidChannels
	}

	dec, err := multistream.NewDecoderDefault(sampleRate, channels)
	if err != nil {
		return nil, err
	}

	return &MultistreamDecoder{
		dec:           dec,
		sampleRate:    sampleRate,
		channels:      channels,
		lastFrameSize: 960, // Default 20ms at 48kHz
	}, nil
}

// Decode decodes an Opus multistream packet into float32 PCM samples.
//
// data: Opus multistream packet data, or nil for Packet Loss Concealment (PLC).
// pcm: Output buffer for decoded samples. Must be large enough to hold
// frameSize * channels samples.
//
// Returns the number of samples per channel decoded, or an error.
//
// When data is nil, the decoder performs packet loss concealment using
// the last successfully decoded frame parameters.
func (d *MultistreamDecoder) Decode(data []byte, pcm []float32) (int, error) {
	// Use last frame size for decoding (PLC or normal)
	frameSize := d.lastFrameSize

	// Validate output buffer size
	needed := frameSize * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	// Decode using internal multistream decoder
	samples, err := d.dec.DecodeToFloat32(data, frameSize)
	if err != nil {
		return 0, err
	}

	// Copy to output buffer
	copy(pcm, samples)

	// Store frame size for PLC
	if data != nil && len(data) > 0 {
		d.lastFrameSize = frameSize
	}

	return frameSize, nil
}

// DecodeInt16 decodes an Opus multistream packet into int16 PCM samples.
//
// data: Opus multistream packet data, or nil for PLC.
// pcm: Output buffer for decoded samples.
//
// Returns the number of samples per channel decoded, or an error.
func (d *MultistreamDecoder) DecodeInt16(data []byte, pcm []int16) (int, error) {
	frameSize := d.lastFrameSize

	// Validate output buffer size
	needed := frameSize * d.channels
	if len(pcm) < needed {
		return 0, ErrBufferTooSmall
	}

	samples, err := d.dec.Decode(data, frameSize)
	if err != nil {
		return 0, err
	}

	// Convert float64 -> int16 with libopus-compatible rounding
	for i := 0; i < frameSize*d.channels; i++ {
		pcm[i] = float64ToInt16(samples[i])
	}

	// Store frame size for PLC
	if data != nil && len(data) > 0 {
		d.lastFrameSize = frameSize
	}

	return frameSize, nil
}

// Reset clears the decoder state for a new stream.
// Call this when starting to decode a new audio stream.
func (d *MultistreamDecoder) Reset() {
	d.dec.Reset()
	d.lastFrameSize = 960
}

// Channels returns the number of audio channels.
func (d *MultistreamDecoder) Channels() int {
	return d.channels
}

// SampleRate returns the sample rate in Hz.
func (d *MultistreamDecoder) SampleRate() int {
	return d.sampleRate
}

// Streams returns the total number of elementary streams.
func (d *MultistreamDecoder) Streams() int {
	return d.dec.Streams()
}

// CoupledStreams returns the number of coupled (stereo) streams.
func (d *MultistreamDecoder) CoupledStreams() int {
	return d.dec.CoupledStreams()
}
