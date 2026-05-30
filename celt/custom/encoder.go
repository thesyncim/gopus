//go:build gopus_custom

package custom

import (
	"errors"

	"github.com/thesyncim/gopus/celt"
)

// bitrateMax mirrors libopus OPUS_BITRATE_MAX (celt SetBitrate sentinel for
// "use the full per-frame byte budget").
const bitrateMax = -1

// encoderErrors for the CustomEncoder.
var (
	ErrEncoderNil       = errors.New("opus custom: nil encoder")
	ErrModeNil          = errors.New("opus custom: nil mode")
	ErrInvalidFrameSize = errors.New("opus custom: invalid frame size for this mode")
	ErrInvalidChannels  = errors.New("opus custom: invalid channel count (must be 1 or 2)")
	ErrInputLength      = errors.New("opus custom: input PCM length does not match frameSize*channels")
	ErrMaxBytes         = errors.New("opus custom: maxBytes must be positive")
	ErrNonStandard      = errors.New("opus custom: non-standard mode requires gopus_custom build; only 48 kHz 120/240/480/960 frames produce byte-identical libopus output")
)

// CustomEncoder holds per-stream encoding state for a CustomMode.
//
// Created via NewEncoder; must not be shared across concurrent goroutines.
// Mirror of libopus OpusCustomEncoder.
type CustomEncoder struct {
	mode     *CustomMode
	channels int
	enc      *celt.Encoder

	// CTL state mirroring libopus encoder_ctl fields.
	bitrate    int
	complexity int
	lsbDepth   int
	vbr        bool
	cvbr       bool
	prediction int
	packetLoss int
}

// NewEncoder creates a new CustomEncoder for the given mode and channel count.
// channels must be 1 or 2.
//
// Reference: libopus celt/celt_encoder.c opus_custom_encoder_create() /
// opus_custom_encoder_init().
func NewEncoder(mode *CustomMode, channels int) (*CustomEncoder, error) {
	if mode == nil {
		return nil, ErrModeNil
	}
	if channels < 1 || channels > 2 {
		return nil, ErrInvalidChannels
	}

	enc := celt.NewEncoder(channels)
	// Disable the Opus-level pre-processing stages that opus_custom_encode does
	// not apply: dc_reject, delay compensation, and lsb-quantization.
	// Reference: libopus celt/celt_encoder.c celt_encode_with_ec() — these
	// stages live in src/opus_encoder.c and are NOT part of celt_encode_with_ec.
	enc.SetDCRejectEnabled(false)
	enc.SetLSBQuantizationEnabled(false)
	enc.SetDelayCompensationEnabled(false)
	// Disable VBR by default (opus_custom defaults to CBR).
	enc.SetVBR(false)

	ce := &CustomEncoder{
		mode:     mode,
		channels: channels,
		enc:      enc,
		// libopus opus_custom_encoder_init() defaults bitrate to OPUS_BITRATE_MAX
		// (celt/celt_encoder.c). In CBR mode the maxBytes argument to
		// opus_custom_encode then becomes the per-frame budget that the encoder
		// fills, rather than a bitrate-derived size. Mirror that with -1.
		bitrate:    bitrateMax,
		complexity: 9,
		lsbDepth:   16,
		vbr:        false,
		cvbr:       false,
		prediction: 2,
		packetLoss: 0,
	}
	// Apply defaults to the inner encoder.
	ce.enc.SetComplexity(ce.complexity)
	ce.enc.SetBitrate(ce.bitrate)
	ce.enc.SetLSBDepth(ce.lsbDepth)

	// Non-standard modes in the Fs==400*shortMdctSize family drive the native
	// CELT data plane parameterized by the mode overlap, short-MDCT scaling base,
	// effEBands clamp and per-rate pre-emphasis.
	if mode.InScaledBandFamily() {
		ce.enc.EnableScaledCustomMode(mode.Fs, mode.Overlap, mode.ShortMdctSize, mode.EffEBands, mode.Preemph)
	}
	return ce, nil
}

// Reset resets the encoder state (equivalent to OPUS_RESET_STATE CTL).
func (ce *CustomEncoder) Reset() {
	if ce == nil {
		return
	}
	ce.enc.Reset()
}

// Mode returns the CustomMode used by this encoder.
func (ce *CustomEncoder) Mode() *CustomMode { return ce.mode }

// Channels returns the channel count.
func (ce *CustomEncoder) Channels() int { return ce.channels }

// EncodeFloat encodes frameSize samples per channel from pcm (float32, range
// −1.0…+1.0, interleaved for stereo) and writes at most maxBytes of compressed
// data. Returns the number of bytes written.
//
// The caller supplies pcm with exactly frameSize*channels samples.
// maxBytes controls the maximum packet size (CBR budget for standard modes).
//
// Standard modes (48 kHz, 120/240/480/960 samples) and the
// Fs==400*shortMdctSize family (e.g. 16000/320, 24000/480) produce output
// byte-identical to libopus --enable-custom-modes. Other non-standard
// (Fs, frame_size) pairs whose band layout is genuinely custom (compute_ebands
// derives a non-48 kHz table) return ErrNonStandard: the gopus CELT encode core
// does not yet thread those per-mode band/allocation/cache tables through its
// data plane. NewMode does compute the full, libopus-exact mode tables for such
// rates (eBands, logN, allocVectors and the compute_pulse_cache index/bits/caps),
// so the remaining native-encode wiring only needs to feed those tables into the
// CELT control plane rather than re-derive them.
//
// Reference: libopus include/opus_custom.h opus_custom_encode_float().
func (ce *CustomEncoder) EncodeFloat(pcm []float32, maxBytes int) ([]byte, error) {
	if ce == nil {
		return nil, ErrEncoderNil
	}
	frameSize := ce.mode.FrameSize
	wantLen := frameSize * ce.channels
	if len(pcm) != wantLen {
		return nil, ErrInputLength
	}
	if maxBytes <= 0 {
		return nil, ErrMaxBytes
	}
	if !ce.mode.isStandard && !ce.mode.InScaledBandFamily() {
		return nil, ErrNonStandard
	}
	ce.enc.SetMaxPayloadBytes(maxBytes)
	return ce.enc.EncodeFrame(pcm, frameSize)
}

// Encode encodes frameSize samples per channel from pcm (int16, native-endian,
// interleaved for stereo). Equivalent to libopus opus_custom_encode().
//
// Reference: libopus include/opus_custom.h opus_custom_encode().
func (ce *CustomEncoder) Encode(pcm []int16, maxBytes int) ([]byte, error) {
	if ce == nil {
		return nil, ErrEncoderNil
	}
	wantLen := ce.mode.FrameSize * ce.channels
	if len(pcm) != wantLen {
		return nil, ErrInputLength
	}
	f := make([]float32, len(pcm))
	for i, v := range pcm {
		f[i] = float32(v) * (1.0 / 32768.0)
	}
	return ce.EncodeFloat(f, maxBytes)
}

// --- CTL setters/getters -------------------------------------------------------

// SetComplexity sets the encoding complexity (0–10).
// Mirrors OPUS_SET_COMPLEXITY via opus_custom_encoder_ctl().
func (ce *CustomEncoder) SetComplexity(c int) error {
	if ce == nil {
		return ErrEncoderNil
	}
	if c < 0 || c > 10 {
		return ErrBadArg
	}
	ce.complexity = c
	ce.enc.SetComplexity(c)
	return nil
}

// Complexity returns the current complexity setting.
func (ce *CustomEncoder) Complexity() int {
	if ce == nil {
		return 0
	}
	return ce.complexity
}

// SetBitrate sets the target bitrate in bits per second, or −1 for max.
// Mirrors OPUS_SET_BITRATE via opus_custom_encoder_ctl().
func (ce *CustomEncoder) SetBitrate(bps int) error {
	if ce == nil {
		return ErrEncoderNil
	}
	ce.bitrate = bps
	ce.enc.SetBitrate(bps)
	return nil
}

// Bitrate returns the current bitrate setting.
func (ce *CustomEncoder) Bitrate() int {
	if ce == nil {
		return 0
	}
	return ce.bitrate
}

// SetVBR enables or disables variable bitrate.
// Mirrors OPUS_SET_VBR via opus_custom_encoder_ctl().
func (ce *CustomEncoder) SetVBR(enabled bool) error {
	if ce == nil {
		return ErrEncoderNil
	}
	ce.vbr = enabled
	ce.enc.SetVBR(enabled)
	return nil
}

// VBR returns whether variable bitrate is enabled.
func (ce *CustomEncoder) VBR() bool {
	if ce == nil {
		return false
	}
	return ce.vbr
}

// SetConstrainedVBR enables or disables constrained VBR.
// Mirrors OPUS_SET_VBR_CONSTRAINT via opus_custom_encoder_ctl().
func (ce *CustomEncoder) SetConstrainedVBR(enabled bool) error {
	if ce == nil {
		return ErrEncoderNil
	}
	ce.cvbr = enabled
	ce.enc.SetConstrainedVBR(enabled)
	return nil
}

// ConstrainedVBR reports whether constrained VBR is enabled.
func (ce *CustomEncoder) ConstrainedVBR() bool {
	if ce == nil {
		return false
	}
	return ce.cvbr
}

// SetPrediction sets the CELT inter-frame prediction mode (0/1/2).
// Mirrors CELT_SET_PREDICTION via opus_custom_encoder_ctl().
func (ce *CustomEncoder) SetPrediction(mode int) error {
	if ce == nil {
		return ErrEncoderNil
	}
	if mode < 0 || mode > 2 {
		return ErrBadArg
	}
	ce.prediction = mode
	ce.enc.SetPrediction(mode)
	return nil
}

// Prediction returns the current prediction mode.
func (ce *CustomEncoder) Prediction() int {
	if ce == nil {
		return 0
	}
	return ce.prediction
}

// SetLSBDepth sets the LSB depth of the input signal (8–24).
// Mirrors OPUS_SET_LSB_DEPTH via opus_custom_encoder_ctl().
func (ce *CustomEncoder) SetLSBDepth(depth int) error {
	if ce == nil {
		return ErrEncoderNil
	}
	if depth < 8 || depth > 24 {
		return ErrBadArg
	}
	ce.lsbDepth = depth
	ce.enc.SetLSBDepth(depth)
	return nil
}

// LSBDepth returns the current LSB depth.
func (ce *CustomEncoder) LSBDepth() int {
	if ce == nil {
		return 0
	}
	return ce.lsbDepth
}

// SetPacketLoss sets the expected packet loss percentage (0–100).
// Mirrors OPUS_SET_PACKET_LOSS_PERC via opus_custom_encoder_ctl().
func (ce *CustomEncoder) SetPacketLoss(lossPercent int) error {
	if ce == nil {
		return ErrEncoderNil
	}
	if lossPercent < 0 || lossPercent > 100 {
		return ErrBadArg
	}
	ce.packetLoss = lossPercent
	ce.enc.SetPacketLoss(lossPercent)
	return nil
}

// PacketLoss returns the current packet loss setting.
func (ce *CustomEncoder) PacketLoss() int {
	if ce == nil {
		return 0
	}
	return ce.packetLoss
}

// FinalRange returns the range coder final state after the last EncodeFloat
// or Encode call. Mirrors OPUS_GET_FINAL_RANGE via opus_custom_encoder_ctl().
func (ce *CustomEncoder) FinalRange() uint32 {
	if ce == nil {
		return 0
	}
	return ce.enc.FinalRange()
}
