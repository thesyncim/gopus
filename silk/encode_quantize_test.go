package silk

import (
	"math"
	"testing"
)

func TestEncodeFrameQuantizesInputPCM(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000
	if frameSamples <= 0 {
		t.Fatalf("invalid frameSamples: %d", frameSamples)
	}

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		pcm[i] = 0.1 // value that requires rounding when scaled to int16
	}

	_ = enc.EncodeFrame(pcm, nil, true)

	fsKHz := config.SampleRate / 1000
	ltpMemSamples := ltpMemLengthMs * fsKHz
	laShapeSamples := laShapeMs * fsKHz
	insertOffset := ltpMemSamples + laShapeSamples
	if insertOffset >= len(enc.inputBuffer) {
		t.Fatalf("insertOffset out of range: %d (buf=%d)", insertOffset, len(enc.inputBuffer))
	}

	expected := float32(floatToInt16Round(pcm[0]*float32(silkSampleScale))) / float32(silkSampleScale)
	got := enc.inputBuffer[insertOffset]
	if math.Abs(float64(got-expected)) > 1e-9 {
		t.Fatalf("input not quantized: got=%v expected=%v", got, expected)
	}
}
