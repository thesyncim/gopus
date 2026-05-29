//go:build gopus_custom

package custom

import (
	"errors"

	"github.com/thesyncim/gopus/celt"
)

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
		mode:       mode,
		channels:   channels,
		enc:        enc,
		bitrate:    64000,
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
// Standard modes (48 kHz, 120/240/480/960 samples) produce output byte-identical
// to libopus. Non-standard modes use the same CELT encoder pipeline with the
// equivalent standard frame size (derived from maxLM); the bitstream will decode
// with this package's CustomDecoder (self-consistent) but will differ from a
// libopus custom-modes build because the band layout and pre-emphasis depend on
// the target sample rate.
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
	ce.enc.SetMaxPayloadBytes(maxBytes)
	if ce.mode.isStandard {
		return ce.enc.EncodeFrame(pcm, frameSize)
	}
	// Non-standard modes: map the frame to the nearest standard CELT frame size
	// that shares the same maxLM (and thus the same short-block structure).
	// The standard sizes indexed by maxLM are: 120 (LM=0), 240 (LM=1),
	// 480 (LM=2), 960 (LM=3).
	// This allows the existing 48 kHz encoder core to produce a self-consistent
	// bitstream that our CustomDecoder can decode.
	stdFrameSize := standardFrameForLM(ce.mode.MaxLM)
	if len(pcm) != stdFrameSize*ce.channels {
		// Resize if needed (the custom frame has the same LM but different
		// total sample count due to different Fs).
		resized := resizePCM(pcm, ce.channels, frameSize, stdFrameSize)
		return ce.enc.EncodeFrame(resized, stdFrameSize)
	}
	return ce.enc.EncodeFrame(pcm, stdFrameSize)
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

// standardFrameForLM returns the standard 48 kHz frame size (in samples) for
// the given maxLM value: LM=0→120, LM=1→240, LM=2→480, LM=3→960.
func standardFrameForLM(maxLM int) int {
	switch maxLM {
	case 0:
		return 120
	case 1:
		return 240
	case 2:
		return 480
	case 3:
		return 960
	default:
		return 960
	}
}

// resizePCM resamples (via linear interpolation) pcm from srcFrames to dstFrames
// per channel.  This is a placeholder that allows non-standard modes to produce
// a valid bitstream without the full sample-rate conversion pipeline.
// For production use at a non-standard sample rate, the caller should perform
// proper sample-rate conversion before calling EncodeFloat.
func resizePCM(pcm []float32, channels, srcFrames, dstFrames int) []float32 {
	out := make([]float32, dstFrames*channels)
	for ch := 0; ch < channels; ch++ {
		for i := 0; i < dstFrames; i++ {
			// Map output position to input position via linear interpolation.
			pos := float64(i) * float64(srcFrames) / float64(dstFrames)
			lo := int(pos)
			hi := lo + 1
			frac := float32(pos - float64(lo))
			var loSample, hiSample float32
			if lo < srcFrames {
				loSample = pcm[lo*channels+ch]
			}
			if hi < srcFrames {
				hiSample = pcm[hi*channels+ch]
			}
			out[i*channels+ch] = loSample*(1-frac) + hiSample*frac
		}
	}
	return out
}
