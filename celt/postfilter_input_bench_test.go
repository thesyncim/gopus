package celt

import (
	"math"
	"testing"
)

func BenchmarkCombFilterWithInputF32(b *testing.B) {
	start := combFilterHistory
	n := 960
	bufLen := start + n + combFilterMaxPeriod + 4
	src := make([]float64, bufLen)
	dst := make([]float64, bufLen)
	for i := range src {
		src[i] = math.Sin(float64(i)*0.013) + 0.25*math.Cos(float64(i)*0.041)
	}
	window := GetWindowBuffer(Overlap)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(dst, src)
		combFilterWithInputF32(dst, src, start, 100, 96, n, 0.4375, 0.3125, 1, 2, window, Overlap)
	}
}
