//go:build gopus_custom

package custom

import (
	"errors"

	"github.com/thesyncim/gopus/internal/celt"
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
	mode     *CustomMode
	channels int
	dec      *celt.Decoder

	// CTL state.
	complexity int
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
	// Decline modes whose band layout exceeds the native data-plane capacity
	// (nbEBands > maxNativeBands). The static history buffers are sized by
	// MaxBands, so a wider per-mode layout would index them out of range and the
	// decode would diverge; returning ErrNonStandard keeps the boundary clean.
	if !mode.nativeSupported() {
		return nil, ErrNonStandard
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
		complexity: 9,
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
		dec.EnableScaledCustomMode(mode.Fs, mode.Overlap, mode.ShortMdctSize, mode.EffEBands, mode.Preemph)
	} else if !mode.isStandard {
		dec.EnableScaledCustomMode(mode.Fs, mode.Overlap, mode.ShortMdctSize, mode.EffEBands, mode.Preemph)
		dec.EnablePerModeTables(mode.NbEBands, mode.ShortMdctSize, mode.EBands, mode.LogN, mode.AllocVectors, mode.CacheIndex, mode.CacheBits, mode.CacheCaps)
	}
	return cd, nil
}

// Reset resets the decoder state (equivalent to OPUS_RESET_STATE CTL).
func (cd *CustomDecoder) Reset() {
	if cd == nil {
		return
	}
	cd.dec.Reset()
}

// Mode returns the CustomMode used by this decoder.
func (cd *CustomDecoder) Mode() *CustomMode { return cd.mode }

// Channels returns the channel count.
func (cd *CustomDecoder) Channels() int { return cd.channels }

// DecodeFloat decodes a compressed frame and returns float32 PCM samples.
// data is the compressed payload (nil or len ≤ 1 triggers PLC).
// frameSize is the expected number of output samples per channel; it must equal
// the mode's FrameSize (or a valid on-the-fly smaller multiple).
//
// Returns frameSize*channels float32 samples, interleaved for stereo.
//
// DecodeFloat decodes sample-identically to libopus --enable-custom-modes
// (within the documented arm64 1-ULP CELT drift) for the standard 48 kHz modes
// and every non-standard mode within the native band-cap (NbEBands <= 21): the
// per-mode band tables (eBands, allocVectors, compute_pulse_cache) computed by
// NewMode are threaded through the CELT decode data plane. Modes with a wider
// band layout are declined at NewDecoder time with ErrNonStandard.
//
// Reference: libopus include/opus_custom.h opus_custom_decode_float().
func (cd *CustomDecoder) DecodeFloat(data []byte, frameSize int) ([]float32, error) {
	if cd == nil {
		return nil, ErrDecoderNil
	}
	if frameSize <= 0 {
		return nil, ErrInvalidFrameSize
	}
	if !cd.mode.isValidDecodeSize(frameSize) {
		return nil, ErrInvalidFrameSize
	}
	return cd.dec.DecodeFrame(data, frameSize)
}

// Decode decodes a compressed frame and returns int16 PCM samples.
// Equivalent to libopus opus_custom_decode().
//
// Reference: libopus include/opus_custom.h opus_custom_decode().
func (cd *CustomDecoder) Decode(data []byte, frameSize int) ([]int16, error) {
	f, err := cd.DecodeFloat(data, frameSize)
	if err != nil {
		return nil, err
	}
	out := make([]int16, len(f))
	for i, v := range f {
		// Soft-clip to [-1, 1] then scale to int16.
		s := v
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		out[i] = int16(s * 32767.0)
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
	return cd.dec.FinalRange()
}
