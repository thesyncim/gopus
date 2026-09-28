//go:build gopus_custom_modes && gopus_fixed_point

package custom

import (
	"errors"

	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

var ErrFixedCustomModeUnsupported = errors.New("opus custom: fixed-point custom mode geometry is unsupported")

type fixedCustomEncoderState struct {
	enc      *fixedpoint.CELTEncoder
	channels int
	coder    rangecoding.Encoder
	packet   []byte
	res      []int32
}

func newFixedCustomEncoder(mode *CustomMode, channels int) (fixedCustomEncoder, error) {
	if !fixedCustomModeSupported(mode) {
		return nil, ErrFixedCustomModeUnsupported
	}
	enc := fixedpoint.NewCELTEncoder(channels)
	if !mode.isStandard {
		enc = newFixedCustomCELTEncoder(channels, fixedCustomModeConfig(mode))
		if enc == nil {
			return nil, ErrFixedCustomModeUnsupported
		}
	}
	enc.SetComplexity(9)
	enc.SetLSBDepth(16)
	enc.SetVBR(false)
	enc.SetConstrainedVBR(false)
	return &fixedCustomEncoderState{enc: enc, channels: channels}, nil
}

func fixedCustomModeSupported(mode *CustomMode) bool {
	if mode.isStandard {
		return mode.Fs == 48000 && mode.ShortMdctSize == 120 &&
			(mode.FrameSize == 120 || mode.FrameSize == 240 || mode.FrameSize == 480 || mode.FrameSize == 960)
	}
	return !customQEXT
}

func fixedCustomModeConfig(mode *CustomMode) fixedpoint.CELTCustomMode {
	return fixedpoint.CELTCustomMode{
		Fs: mode.Fs, FrameSize: mode.FrameSize,
		ShortMdctSize: mode.ShortMdctSize, Overlap: mode.Overlap,
		MaxLM: mode.MaxLM, EffEBands: mode.EffEBands,
		EBands: mode.EBands, LogN: mode.LogN, AllocVectors: mode.AllocVectors,
		CacheIndex: mode.CacheIndex, CacheBits: mode.CacheBits, CacheCaps: mode.CacheCaps,
		ScaledBandFamily: mode.InScaledBandFamily(),
	}
}

func (s *fixedCustomEncoderState) reset() { s.enc.Reset() }

func (s *fixedCustomEncoderState) finalRange() uint32 { return s.enc.FinalRange() }

func (s *fixedCustomEncoderState) setComplexity(c int) { s.enc.SetComplexity(c) }

func (s *fixedCustomEncoderState) setBitrate(b int) { s.enc.SetBitrate(b) }

func (s *fixedCustomEncoderState) setVBR(v bool) { s.enc.SetVBR(v) }

func (s *fixedCustomEncoderState) setConstrainedVBR(v bool) { s.enc.SetConstrainedVBR(v) }

func (s *fixedCustomEncoderState) setPrediction(mode int) { s.enc.SetPrediction(int32(mode)) }

func (s *fixedCustomEncoderState) setLSBDepth(depth int) { s.enc.SetLSBDepth(depth) }

func (s *fixedCustomEncoderState) setPacketLoss(percent int) { s.enc.SetPacketLoss(percent) }

func (s *fixedCustomEncoderState) ensureRes(n int) []int32 {
	if cap(s.res) < n {
		s.res = make([]int32, n)
	}
	s.res = s.res[:n]
	return s.res
}

func (s *fixedCustomEncoderState) encodeFloat(pcm []float32, maxBytes int) ([]byte, error) {
	res := s.ensureRes(len(pcm))
	for i, sample := range pcm {
		res[i] = fixedCustomFloatToRes(sample)
	}
	return s.encodeRes(res, len(pcm)/s.channels, maxBytes)
}

// fixedCustomFloatToRes mirrors celt/float_cast.h FLOAT2INT24: the scale and
// clamps stay in float32, followed by the target's round-to-nearest-even step.
func fixedCustomFloatToRes(sample float32) int32 {
	x := sample * (32768 * 256)
	if x != x {
		x = -16777216
	}
	x = max(-16777216, min(16777216, x))
	whole := int32(x)
	fraction := x - float32(whole)
	if fraction > .5 || fraction == .5 && whole&1 != 0 {
		whole++
	} else if fraction < -.5 || fraction == -.5 && whole&1 != 0 {
		whole--
	}
	return whole
}

func (s *fixedCustomEncoderState) encodeShort(pcm []int16, maxBytes int) ([]byte, error) {
	res := s.ensureRes(len(pcm))
	for i, sample := range pcm {
		res[i] = int32(sample) << 8
	}
	return s.encodeRes(res, len(pcm)/s.channels, maxBytes)
}

func (s *fixedCustomEncoderState) encodeRes(res []int32, frameSize, maxBytes int) ([]byte, error) {
	if cap(s.packet) < maxBytes {
		s.packet = make([]byte, maxBytes)
	}
	s.packet = s.packet[:maxBytes]
	clear(s.packet)
	s.coder.Init(s.packet)
	n := s.enc.EncodeWithECRes(res, frameSize, &s.coder, maxBytes)
	if n < 0 || n > len(s.packet) {
		return nil, ErrBadArg
	}
	return s.coder.Buffer()[:n], nil
}
