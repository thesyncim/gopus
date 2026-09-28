//go:build gopus_custom_modes

package custom

import (
	"errors"

	"github.com/thesyncim/gopus/internal/celt"
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
	ErrInvalidBandCount = errors.New("opus custom: invalid mode band count")
	ErrInvalidPacket    = errors.New("opus custom: invalid signalled packet")
)

// CustomEncoder holds per-stream encoding state for a CustomMode.
//
// Created via NewEncoder; must not be shared across concurrent goroutines.
// Mirror of libopus OpusCustomEncoder.
type CustomEncoder struct {
	mode       *CustomMode
	channels   int
	enc        *celt.Encoder
	fixed      fixedCustomEncoder
	packet     []byte
	int16PCM   []float32
	signalling bool

	// CTL state mirroring libopus encoder_ctl fields.
	bitrate    int
	complexity int
	lsbDepth   int
	vbr        bool
	cvbr       bool
	prediction int
	packetLoss int
}

type fixedCustomEncoder interface {
	encodeFloat([]float32, int) ([]byte, error)
	encodeShort([]int16, int) ([]byte, error)
	reset()
	finalRange() uint32
	setComplexity(int)
	setBitrate(int)
	setVBR(bool)
	setConstrainedVBR(bool)
	setSignalling(bool)
	setPrediction(int)
	setLSBDepth(int)
	setPacketLoss(int)
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
	if !mode.nativeSupported() {
		return nil, ErrInvalidBandCount
	}
	fixed, err := newFixedCustomEncoder(mode, channels)
	if err != nil {
		return nil, err
	}
	if fixed != nil {
		fixed.setSignalling(true)
		return &CustomEncoder{
			mode: mode, channels: channels, fixed: fixed,
			signalling: true,
			bitrate:    bitrateMax, complexity: 5, lsbDepth: 24,
			cvbr:       true,
			prediction: 2,
		}, nil
	}

	enc := celt.NewEncoder(channels)
	// Disable the Opus-level pre-processing stages that opus_custom_encode does
	// not apply: dc_reject, delay compensation, and lsb-quantization.
	// Reference: libopus celt/celt_encoder.c celt_encode_with_ec() — these
	// stages live in src/opus_encoder.c and are NOT part of celt_encode_with_ec.
	enc.SetDCRejectEnabled(false)
	enc.SetLSBQuantizationEnabled(false)
	enc.SetDelayCompensationEnabled(false)
	enc.SetCustomSignalling(true)
	// opus_custom_encoder_init_arch starts in CBR mode with constrained VBR
	// enabled for when a caller turns VBR on.
	enc.SetVBR(false)
	enc.SetConstrainedVBR(true)

	ce := &CustomEncoder{
		mode:     mode,
		channels: channels,
		enc:      enc,
		// libopus opus_custom_encoder_init() defaults bitrate to OPUS_BITRATE_MAX
		// (celt/celt_encoder.c). In CBR mode the maxBytes argument to
		// opus_custom_encode then becomes the per-frame budget that the encoder
		// fills, rather than a bitrate-derived size. Mirror that with -1.
		bitrate:    bitrateMax,
		complexity: 5,
		lsbDepth:   24,
		vbr:        false,
		cvbr:       true,
		prediction: 2,
		packetLoss: 0,
		signalling: true,
	}
	// Apply opus_custom_encoder_init_arch defaults to the inner encoder.
	ce.enc.SetComplexity(ce.complexity)
	ce.enc.SetBitrate(ce.bitrate)
	ce.enc.SetLSBDepth(ce.lsbDepth)

	// Non-standard modes in the Fs==400*shortMdctSize family drive the native
	// CELT data plane parameterized by the mode overlap, short-MDCT scaling base,
	// effEBands clamp and per-rate pre-emphasis. They reuse the static 21-band
	// 48 kHz tables.
	//
	// Non-standard modes OUTSIDE that family (e.g. 48000/640, NbEBands=19) have a
	// genuinely custom band layout. They drive the same overlap/scale/pre-emphasis
	// machinery PLUS the per-mode band tables (edges, widths, logN, allocVectors,
	// pulse cache) installed via EnablePerModeTables. This mirrors the symmetric
	// decode wiring in NewDecoder.
	if mode.InScaledBandFamily() {
		ce.enc.EnableScaledCustomMode(mode.Fs, mode.Overlap, mode.ShortMdctSize, mode.EffEBands, mode.Preemph, mode.transforms)
	} else if !mode.isStandard {
		ce.enc.EnableScaledCustomMode(mode.Fs, mode.Overlap, mode.ShortMdctSize, mode.EffEBands, mode.Preemph, mode.transforms)
		ce.enc.EnablePerModeTables(mode.NbEBands, mode.ShortMdctSize, mode.EBands, mode.LogN, mode.AllocVectors, mode.CacheIndex, mode.CacheBits, mode.CacheCaps)
	}
	return ce, nil
}

// Reset resets the encoder state (equivalent to OPUS_RESET_STATE CTL).
func (ce *CustomEncoder) Reset() {
	if ce == nil {
		return
	}
	if ce.fixed != nil {
		ce.fixed.reset()
		return
	}
	ce.enc.Reset()
}

// Mode returns the CustomMode used by this encoder.
func (ce *CustomEncoder) Mode() *CustomMode { return ce.mode }

// Channels returns the channel count.
func (ce *CustomEncoder) Channels() int { return ce.channels }

// SetSignalling enables or disables the one-byte custom frame header.
// NewEncoder enables it by default, matching opus_custom_encoder_create().
// Disable it only when encoding a raw CELT payload for a caller that supplies
// frame size and channel count out of band.
func (ce *CustomEncoder) SetSignalling(enabled bool) error {
	if ce == nil {
		return ErrEncoderNil
	}
	ce.signalling = enabled
	if ce.fixed != nil {
		ce.fixed.setSignalling(enabled)
	} else {
		ce.enc.SetCustomSignalling(enabled)
	}
	return nil
}

// Signalling reports whether EncodeFloat and Encode prepend the custom frame
// header.
func (ce *CustomEncoder) Signalling() bool {
	return ce != nil && ce.signalling
}

// EncodeFloat encodes frameSize samples per channel from pcm (float32, range
// −1.0…+1.0, interleaved for stereo) and writes at most maxBytes of compressed
// data. Returns the encoded packet.
//
// The caller supplies pcm with exactly frameSize*channels samples.
// maxBytes controls the maximum packet size and the CBR budget.
//
// Encoding uses the mode's band edges, allocation tables, pulse cache, window,
// and history stride. The returned packet borrows encoder scratch and remains
// valid until the next encode call.
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
	payloadBytes := maxBytes
	if ce.signalling {
		payloadBytes--
		if payloadBytes <= 0 {
			return nil, ErrMaxBytes
		}
		ce.reserveSignallingPacket(maxBytes)
	}
	if ce.fixed != nil {
		packet, err := ce.fixed.encodeFloat(pcm, payloadBytes)
		if err != nil || !ce.signalling {
			return packet, err
		}
		return ce.prependSignallingHeader(packet)
	}
	ce.enc.SetMaxPayloadBytes(payloadBytes)
	packet, err := ce.enc.EncodeFrame(pcm, frameSize)
	if err != nil || !ce.signalling {
		return packet, err
	}
	return ce.prependSignallingHeader(packet)
}

func (ce *CustomEncoder) prependSignallingHeader(payload []byte) ([]byte, error) {
	header, err := customSignallingHeader(ce.mode, ce.channels, ce.mode.FrameSize)
	if err != nil {
		return nil, err
	}
	if cap(ce.packet) < len(payload)+1 {
		ce.packet = make([]byte, len(payload)+1)
	}
	ce.packet = ce.packet[:len(payload)+1]
	ce.packet[0] = header
	copy(ce.packet[1:], payload)
	return ce.packet, nil
}

func (ce *CustomEncoder) reserveSignallingPacket(maxBytes int) {
	capacity := min(maxBytes, customSignallingPacketLimit())
	if cap(ce.packet) < capacity {
		ce.packet = make([]byte, 0, capacity)
	}
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
	if maxBytes <= 0 {
		return nil, ErrMaxBytes
	}
	if ce.fixed != nil {
		payloadBytes := maxBytes
		if ce.signalling {
			payloadBytes--
			if payloadBytes <= 0 {
				return nil, ErrMaxBytes
			}
			ce.reserveSignallingPacket(maxBytes)
		}
		packet, err := ce.fixed.encodeShort(pcm, payloadBytes)
		if err != nil || !ce.signalling {
			return packet, err
		}
		return ce.prependSignallingHeader(packet)
	}
	if cap(ce.int16PCM) < len(pcm) {
		ce.int16PCM = make([]float32, len(pcm))
	}
	f := ce.int16PCM[:len(pcm)]
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
	if ce.fixed != nil {
		ce.fixed.setComplexity(c)
		return nil
	}
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
// Mirrors OPUS_SET_BITRATE via opus_custom_encoder_ctl(): rates of 500 b/s or
// less are rejected and rates above 750 kb/s per channel are capped.
func (ce *CustomEncoder) SetBitrate(bps int) error {
	if ce == nil {
		return ErrEncoderNil
	}
	if bps <= 500 && bps != bitrateMax {
		return ErrBadArg
	}
	ce.bitrate = min(bps, 750000*ce.channels)
	if ce.fixed != nil {
		ce.fixed.setBitrate(ce.bitrate)
		return nil
	}
	ce.enc.SetBitrate(ce.bitrate)
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
	if ce.fixed != nil {
		ce.fixed.setVBR(enabled)
		return nil
	}
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
	if ce.fixed != nil {
		ce.fixed.setConstrainedVBR(enabled)
		return nil
	}
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
	if ce.fixed != nil {
		ce.fixed.setPrediction(mode)
		return nil
	}
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
	if ce.fixed != nil {
		ce.fixed.setLSBDepth(depth)
		return nil
	}
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
	if ce.fixed != nil {
		ce.fixed.setPacketLoss(lossPercent)
		return nil
	}
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
	if ce.fixed != nil {
		return ce.fixed.finalRange()
	}
	return ce.enc.FinalRange()
}
