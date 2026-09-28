//go:build !gopus_qext

package multistream

import "github.com/thesyncim/gopus/internal/encoder"

func (e *Encoder) encodeNativeHD96kStream(_ *encoder.Encoder, _ []float32, _ int, _ int, _ bool) ([]byte, error) {
	return nil, ErrInvalidSampleRate
}
