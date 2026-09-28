//go:build gopus_qext

package multistream

import (
	"github.com/thesyncim/gopus/internal/encoder"
)

func (e *Encoder) encodeNativeHD96kStream(enc *encoder.Encoder, pcm []float32, frameSize, maxDataBytes int, shortInput bool) ([]byte, error) {
	if maxDataBytes < 1 {
		return nil, encoder.ErrInvalidConfig
	}
	var packet []byte
	var err error
	if shortInput {
		packet, err = enc.EncodeShortMixedWithAnalysisMaxBytes(pcm, frameSize, pcm, maxDataBytes)
	} else {
		packet, err = enc.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, maxDataBytes)
	}
	if err != nil {
		return nil, err
	}
	return packet, nil
}
