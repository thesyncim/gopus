package encoder_test

import "github.com/thesyncim/gopus/internal/encoder"

type testFloatPCM interface {
	~float32 | ~float64
}

func testPCM32[T testFloatPCM](pcm []T) []float32 {
	if pcm == nil {
		return nil
	}
	if pcm32, ok := any(pcm).([]float32); ok {
		return pcm32
	}
	out := make([]float32, len(pcm))
	for i, v := range pcm {
		out[i] = float32(v)
	}
	return out
}

func encodeTest[T testFloatPCM](enc *encoder.Encoder, pcm []T, frameSize int) ([]byte, error) {
	return enc.Encode(testPCM32(pcm), frameSize)
}
