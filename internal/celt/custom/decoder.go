//go:build gopus_custom_modes

package custom

import (
	"errors"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/opusmath"
)

// decoderErrors for the CustomDecoder.
var (
	ErrDecoderNil = errors.New("opus custom: nil decoder")
)

// CustomDecoder holds per-stream decoding state for a CustomMode.
//
// Created via NewDecoder; must not be shared across concurrent goroutines.
// Mirror of libopus OpusCustomDecoder.
type CustomDecoder struct {
	mode       *CustomMode
	channels   int
	dec        *celt.Decoder
	fixed      fixedCustomDecoder
	signalling bool

	// CTL state.
	complexity int
}

type fixedCustomDecoder interface {
	decodeFloat([]byte, int, int) ([]float32, error)
	decodeShort([]byte, int, int) ([]int16, error)
	setEndBand(int)
	setQEXTPayload([]byte)
	reset()
	finalRange() uint32
}

// NewDecoder creates a new CustomDecoder for the given mode and channel count.
// channels must be 1 or 2.
//
// Reference: libopus celt/celt_decoder.c opus_custom_decoder_create() /
// opus_custom_decoder_init().
func NewDecoder(mode *CustomMode, channels int) (*CustomDecoder, error) {
	if mode == nil {
		return nil, ErrModeNil
	}
	if channels < 1 || channels > 2 {
		return nil, ErrInvalidChannels
	}
	if !mode.nativeSupported() {
		return nil, ErrInvalidBandCount
	}
	fixed, err := newFixedCustomDecoder(mode, channels)
	if err != nil {
		return nil, err
	}
	if fixed != nil {
		return &CustomDecoder{mode: mode, channels: channels, fixed: fixed, signalling: true}, nil
	}

	dec := celt.NewDecoder(channels)
	// Custom decoder always operates at the mode's native sample rate; we tell
	// the inner celt.Decoder to output at 48 kHz (downsample=1) and let the
	// caller handle sample-rate conversion if Fs != 48000.
	// This mirrors libopus behaviour where the custom decoder decodes at the
	// native rate directly.
	_ = dec.SetAPISampleRate(48000)

	cd := &CustomDecoder{
		mode:       mode,
		channels:   channels,
		dec:        dec,
		signalling: true,
	}
	// Non-standard modes in the Fs==400*shortMdctSize family drive the native
	// CELT decode data plane parameterized by the mode overlap, short-MDCT
	// scaling base, effEBands clamp and per-rate de-emphasis. They reuse the
	// static 21-band 48 kHz tables (computeEBands returns the 5 ms table for
	// them).
	//
	// Non-standard modes OUTSIDE that family (e.g. 48000/640, NbEBands=19) have a
	// genuinely custom band layout. They drive the same overlap/scale/de-emphasis
	// machinery PLUS the per-mode band tables (edges, widths, logN, allocVectors,
	// pulse cache) installed via EnablePerModeTables.
	if mode.InScaledBandFamily() {
		dec.EnableScaledCustomMode(mode.Fs, mode.Overlap, mode.ShortMdctSize, mode.FrameSize, mode.EffEBands, mode.Preemph, mode.transforms)
	} else if !mode.isStandard {
		dec.EnableScaledCustomMode(mode.Fs, mode.Overlap, mode.ShortMdctSize, mode.FrameSize, mode.EffEBands, mode.Preemph, mode.transforms)
		dec.EnablePerModeTables(mode.NbEBands, mode.ShortMdctSize, mode.EBands, mode.LogN, mode.AllocVectors, mode.CacheIndex, mode.CacheBits, mode.CacheCaps)
	}
	return cd, nil
}

// Reset resets the decoder state (equivalent to OPUS_RESET_STATE CTL).
func (cd *CustomDecoder) Reset() {
	if cd == nil {
		return
	}
	if cd.fixed != nil {
		cd.fixed.reset()
		return
	}
	cd.dec.Reset()
}

// Mode returns the CustomMode used by this decoder.
func (cd *CustomDecoder) Mode() *CustomMode { return cd.mode }

// Channels returns the channel count.
func (cd *CustomDecoder) Channels() int { return cd.channels }

// SetSignalling enables or disables parsing the one-byte frame header. The
// header form depends on the build and mode.
// NewDecoder enables it by default, matching opus_custom_decoder_create().
// Disable it only when decoding a raw CELT payload whose frame size and
// channel count are supplied out of band.
func (cd *CustomDecoder) SetSignalling(enabled bool) error {
	if cd == nil {
		return ErrDecoderNil
	}
	cd.signalling = enabled
	return nil
}

// Signalling reports whether DecodeFloat and Decode parse the custom frame
// header.
func (cd *CustomDecoder) Signalling() bool {
	return cd != nil && cd.signalling
}

// DecodeFloat decodes a compressed frame and returns float32 PCM samples.
// data is a signalled custom packet by default; nil or len ≤ 1 triggers PLC.
// SetSignalling(false) selects raw CELT payloads instead. For signalled packets,
// frameSize is the output capacity per channel and the header selects the
// decoded frame size. For raw payloads, frameSize must be the mode's FrameSize
// or a valid on-the-fly smaller multiple.
//
// Returns the decoded frame's samples, interleaved for stereo. A signalled
// frame can be shorter than frameSize, which is the output capacity per
// channel.
//
// Decoding and concealment use the mode's band edges, window, and history
// stride. The returned samples borrow decoder scratch and remain valid until
// the next decode call. Oracle tests check exact PCM and final ranges against
// the selected libopus --enable-custom-modes build.
//
// Reference: libopus include/opus_custom.h opus_custom_decode_float().
func (cd *CustomDecoder) DecodeFloat(data []byte, frameSize int) ([]float32, error) {
	if cd == nil {
		return nil, ErrDecoderNil
	}
	if (!cd.signalling || len(data) == 0) && !cd.mode.isValidDecodeSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}
	if cd.fixed != nil {
		if cd.signalling && len(data) > 0 {
			frame, err := parseCustomSignallingPacket(cd.mode, data, frameSize)
			if frame.endBand > 0 {
				cd.fixed.setEndBand(frame.endBand)
			}
			if err != nil {
				return nil, err
			}
			cd.fixed.setQEXTPayload(frame.qextPayload(data))
			return cd.fixed.decodeFloat(frame.payload(data), frame.frameSize, frame.channels)
		}
		return cd.fixed.decodeFloat(data, frameSize, cd.channels)
	}
	if cd.signalling && len(data) > 0 {
		frame, err := parseCustomSignallingPacket(cd.mode, data, frameSize)
		if frame.endBand > 0 {
			cd.dec.SetCustomEndBand(frame.endBand)
		}
		if err != nil {
			return nil, err
		}
		setCustomQEXTPayload(cd.dec, frame.qextPayload(data))
		return cd.dec.DecodeFrameWithPacketStereo(frame.payload(data), frame.frameSize, frame.channels == 2)
	}
	return cd.dec.DecodeFrame(data, frameSize)
}

// Decode decodes a compressed frame and returns int16 PCM samples.
// Equivalent to libopus opus_custom_decode().
//
// Reference: libopus include/opus_custom.h opus_custom_decode().
func (cd *CustomDecoder) Decode(data []byte, frameSize int) ([]int16, error) {
	if cd == nil {
		return nil, ErrDecoderNil
	}
	if (!cd.signalling || len(data) == 0) && !cd.mode.isValidDecodeSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}
	if cd.fixed != nil {
		if cd.signalling && len(data) > 0 {
			frame, err := parseCustomSignallingPacket(cd.mode, data, frameSize)
			if frame.endBand > 0 {
				cd.fixed.setEndBand(frame.endBand)
			}
			if err != nil {
				return nil, err
			}
			cd.fixed.setQEXTPayload(frame.qextPayload(data))
			return cd.fixed.decodeShort(frame.payload(data), frame.frameSize, frame.channels)
		}
		return cd.fixed.decodeShort(data, frameSize, cd.channels)
	}
	f, err := cd.DecodeFloat(data, frameSize)
	if err != nil {
		return nil, err
	}
	out := make([]int16, len(f))
	for i, v := range f {
		// opus_custom_decode applies RES2INT16: scale by 32768, saturate,
		// and round to even (celt/celt_decoder.c, celt/float_cast.h).
		out[i] = opusmath.Float32ToInt16(v)
	}
	return out, nil
}

// isValidDecodeSize returns true if sz is the mode's FrameSize or any
// valid sub-frame (FrameSize >> j for j in 0..MaxLM, all multiples of 2).
func (m *CustomMode) isValidDecodeSize(sz int) bool {
	for j := 0; j <= m.MaxLM; j++ {
		if sz == m.FrameSize>>j {
			return true
		}
	}
	return false
}

// --- CTL setters/getters -------------------------------------------------------

// SetComplexity sets the decoder complexity (0–10).
// Mirrors OPUS_SET_COMPLEXITY via opus_custom_decoder_ctl().
func (cd *CustomDecoder) SetComplexity(c int) error {
	if cd == nil {
		return ErrDecoderNil
	}
	if c < 0 || c > 10 {
		return ErrBadArg
	}
	cd.complexity = c
	if cd.fixed != nil {
		return nil
	}
	return cd.dec.SetComplexity(c)
}

// Complexity returns the current decoder complexity setting.
func (cd *CustomDecoder) Complexity() int {
	if cd == nil {
		return 0
	}
	return cd.complexity
}

// FinalRange returns the range coder final state after the last Decode call.
// Mirrors OPUS_GET_FINAL_RANGE via opus_custom_decoder_ctl().
func (cd *CustomDecoder) FinalRange() uint32 {
	if cd == nil {
		return 0
	}
	if cd.fixed != nil {
		return cd.fixed.finalRange()
	}
	return cd.dec.FinalRange()
}
