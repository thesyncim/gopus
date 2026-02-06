package celt

import (
	"math"
	"testing"
)

func benchmarkExpRotation1(b *testing.B, length, stride int) {
	b.Helper()
	if length <= 0 || stride <= 0 {
		b.Fatal("invalid benchmark params")
	}
	x := make([]float64, length+stride+8)
	for i := range x {
		x[i] = math.Sin(float64(i) * 0.137)
	}
	c := 0.9238795325 // cos(pi/8)
	s := 0.3826834324 // sin(pi/8)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		expRotation1(x, length, stride, c, s)
	}
}

func BenchmarkExpRotation1Stride1Len32(b *testing.B) { benchmarkExpRotation1(b, 32, 1) }
func BenchmarkExpRotation1Stride1Len64(b *testing.B) { benchmarkExpRotation1(b, 64, 1) }
func BenchmarkExpRotation1Stride2Len32(b *testing.B) { benchmarkExpRotation1(b, 32, 2) }
func BenchmarkExpRotation1Stride4Len32(b *testing.B) { benchmarkExpRotation1(b, 32, 4) }
