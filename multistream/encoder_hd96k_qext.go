//go:build gopus_qext

package multistream

import (
	"github.com/thesyncim/gopus/internal/encoder"
)

func (e *Encoder) encodeNativeHD96kStream(enc *encoder.Encoder, pcm []float32, frameSize, maxDataBytes int, shortInput bool) ([]byte, error) {
	if frameSize != 240 && frameSize != 480 && frameSize != 960 && frameSize != 1920 {
		return nil, encoder.ErrInvalidFrameSize
	}
	if maxDataBytes < 3 {
		return nil, encoder.ErrInvalidConfig
	}
	if cap(e.hd96k.packet) < maxDataBytes {
		e.hd96k.packet = make([]byte, maxDataBytes)
	} else {
		e.hd96k.packet = e.hd96k.packet[:maxDataBytes]
	}

	depth := enc.LSBDepth()
	if shortInput && depth > 16 {
		enc.SetLSBDepth(16)
	}
	n, err := enc.EncodeNativeHD96k(pcm, frameSize, e.hd96k.packet)
	if shortInput && depth > 16 {
		enc.SetLSBDepth(depth)
	}
	if err != nil {
		return nil, err
	}
	return e.hd96k.packet[:n], nil
}
