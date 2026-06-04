package celt

import (
	"math/rand"
	"testing"
)

var pitchDownsampleSink32 float32

func benchmarkPitchDownsampleF32(b *testing.B, channels int, fn func([]float64, []float32, int, int, int)) {
	rng := rand.New(rand.NewSource(43))
	length := (combFilterMaxPeriod + 480) >> 1
	xLen := 2 * length
	if channels == 2 {
		xLen *= 2
	}
	x := make([]float64, xLen)
	for i := range x {
		x[i] = rng.Float64()*2 - 1
	}
	xLP := make([]float32, length)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn(x, xLP, length, channels, 2)
	}
	pitchDownsampleSink32 = xLP[length-1]
}

func BenchmarkPitchDownsampleCurrentMono(b *testing.B) {
	benchmarkPitchDownsampleF32(b, 1, pitchDownsample)
}

func BenchmarkPitchDownsampleCurrentStereo(b *testing.B) {
	benchmarkPitchDownsampleF32(b, 2, pitchDownsample)
}
