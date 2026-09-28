//go:build gopus_custom_modes && gopus_fixed_point && gopus_qext

package custom

import (
	"github.com/thesyncim/gopus/internal/fixedpoint"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

type fixedCustomDecoderState struct {
	dec      *fixedpoint.QEXTCELTDecoder
	channels int
	reader   rangecoding.Decoder
	res      []int32
	floatPCM []float32
	shortPCM []int16
}

func newFixedCustomDecoder(mode *CustomMode, channels int) (fixedCustomDecoder, error) {
	if !fixedCustomModeSupported(mode) {
		return nil, ErrFixedCustomModeUnsupported
	}
	dec, err := fixedpoint.NewQEXTCELTDecoder(channels, 48000)
	if err != nil {
		return nil, err
	}
	return &fixedCustomDecoderState{dec: dec, channels: channels}, nil
}

func (s *fixedCustomDecoderState) reset() { s.dec.Reset() }

func (s *fixedCustomDecoderState) finalRange() uint32 { return s.dec.FinalRange() }

func (s *fixedCustomDecoderState) decodeRes(data []byte, frameSize int) ([]int32, error) {
	n := frameSize * s.channels
	if cap(s.res) < n {
		s.res = make([]int32, n)
	}
	s.res = s.res[:n]
	var reader *rangecoding.Decoder
	if len(data) > 1 {
		s.reader.Init(data)
		reader = &s.reader
	}
	if got := s.dec.DecodeFrameWithEC(reader, len(data), frameSize, s.channels, nil, s.res); got != frameSize {
		return nil, ErrBadArg
	}
	return s.res, nil
}

func (s *fixedCustomDecoderState) decodeFloat(data []byte, frameSize int) ([]float32, error) {
	res, err := s.decodeRes(data, frameSize)
	if err != nil {
		return nil, err
	}
	if cap(s.floatPCM) < len(res) {
		s.floatPCM = make([]float32, len(res))
	}
	s.floatPCM = s.floatPCM[:len(res)]
	for i, sample := range res {
		s.floatPCM[i] = float32(sample) * (1.0 / 32768.0 / 256.0)
	}
	return s.floatPCM, nil
}

func (s *fixedCustomDecoderState) decodeShort(data []byte, frameSize int) ([]int16, error) {
	res, err := s.decodeRes(data, frameSize)
	if err != nil {
		return nil, err
	}
	if cap(s.shortPCM) < len(res) {
		s.shortPCM = make([]int16, len(res))
	}
	s.shortPCM = s.shortPCM[:len(res)]
	for i, sample := range res {
		s.shortPCM[i] = fixedpoint.Res2Int16(sample)
	}
	return s.shortPCM, nil
}
