package celt

import (
	"math"
	"testing"
)

func BenchmarkCombFilterWithInputF32(b *testing.B) {
	start := combFilterHistory
	n := 960
	bufLen := start + n + combFilterMaxPeriod + 4
	src := make([]celtSig, bufLen)
	dst := make([]celtSig, bufLen)
	for i := range src {
		src[i] = celtSig(float32(math.Sin(float64(i)*0.013) + 0.25*math.Cos(float64(i)*0.041)))
	}
	window := GetWindowBufferF32(Overlap)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(dst, src)
		combFilterWithInputSig(dst, src, start, 100, 96, n, 0.4375, 0.3125, 1, 2, window, Overlap)
	}
}
