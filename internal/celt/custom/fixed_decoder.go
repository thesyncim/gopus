//go:build gopus_custom_modes && gopus_fixed_point && !gopus_qext

package custom

import "github.com/thesyncim/gopus/internal/fixedpoint"

type fixedCustomDecoderState struct {
	dec      *fixedpoint.CELTDecoder
	channels int
	floatPCM []float32
	shortPCM []int16
}

func newFixedCustomDecoder(mode *CustomMode, channels int) (fixedCustomDecoder, error) {
	if !fixedCustomModeSupported(mode) {
		return nil, ErrFixedCustomModeUnsupported
	}
	dec := fixedpoint.NewCELTDecoder(channels)
	if !mode.isStandard {
		dec = fixedpoint.NewCELTDecoderCustom(channels, fixedCustomModeConfig(mode))
		if dec == nil {
			return nil, ErrFixedCustomModeUnsupported
		}
	}
	return &fixedCustomDecoderState{dec: dec, channels: channels}, nil
}

func (s *fixedCustomDecoderState) reset() { s.dec.Reset() }

func (s *fixedCustomDecoderState) setEndBand(end int) { s.dec.SetBandRange(0, end) }

func (s *fixedCustomDecoderState) setQEXTPayload([]byte) {}

func (s *fixedCustomDecoderState) finalRange() uint32 { return s.dec.FinalRange() }

func (s *fixedCustomDecoderState) decodeRes(data []byte, frameSize, codedChannels int) ([]int32, error) {
	n := frameSize * s.channels
	if cap(s.shortPCM) < n {
		s.shortPCM = make([]int16, n)
	}
	s.shortPCM = s.shortPCM[:n]
	if got := s.dec.DecodeWithECChannels(data, frameSize, codedChannels, s.shortPCM); got != frameSize {
		return nil, ErrBadArg
	}
	return s.dec.LastRes(), nil
}

func (s *fixedCustomDecoderState) decodeFloat(data []byte, frameSize, codedChannels int) ([]float32, error) {
	res, err := s.decodeRes(data, frameSize, codedChannels)
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

func (s *fixedCustomDecoderState) decodeShort(data []byte, frameSize, codedChannels int) ([]int16, error) {
	if _, err := s.decodeRes(data, frameSize, codedChannels); err != nil {
		return nil, err
	}
	return s.shortPCM, nil
}
