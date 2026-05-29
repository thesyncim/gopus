//go:build gopus_custom

package custom

import (
	"errors"

	"github.com/thesyncim/gopus/celt"
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

	dec := celt.NewDecoder(channels)
	// Custom decoder always operates at the mode's native sample rate; we tell
	// the inner celt.Decoder to output at 48 kHz (downsample=1) and let the
	// caller handle sample-rate conversion if Fs != 48000.
	// This mirrors libopus behaviour where the custom decoder decodes at the
	// native rate directly.
	_ = dec.SetAPISampleRate(48000)

	return &CustomDecoder{
		mode:       mode,
		channels:   channels,
		dec:        dec,
		complexity: 9,
	}, nil
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
// For non-standard modes, the packet was encoded using the equivalent standard
// frame size (standardFrameForLM(mode.MaxLM)), so this decoder uses that standard
// size internally and returns the decoded samples. The output length matches the
// mode's FrameSize to maintain the API contract.
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
	if cd.mode.isStandard {
		return cd.dec.DecodeFrame(data, frameSize)
	}
	// Non-standard mode: the packet was produced with the standard frame size
	// corresponding to maxLM. Decode using that size and resample back to
	// the caller's requested frameSize.
	stdFrameSize := standardFrameForLMDec(cd.mode.MaxLM)
	decoded, err := cd.dec.DecodeFrame(data, stdFrameSize)
	if err != nil {
		return nil, err
	}
	// Resample to the requested frameSize if different.
	if stdFrameSize == frameSize {
		return decoded, nil
	}
	channels := cd.channels
	return resizePCMDec(decoded, channels, stdFrameSize, frameSize), nil
}

// standardFrameForLMDec mirrors encoder.standardFrameForLM for the decode path.
func standardFrameForLMDec(maxLM int) int {
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

// resizePCMDec resamples pcm from srcFrames to dstFrames per channel using
// linear interpolation. Mirrors the encoder-side resizePCM for the decode path.
func resizePCMDec(pcm []float32, channels, srcFrames, dstFrames int) []float32 {
	out := make([]float32, dstFrames*channels)
	for ch := 0; ch < channels; ch++ {
		for i := 0; i < dstFrames; i++ {
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
