package celt

import (
	"math/rand"
	"testing"
)

var haar1BenchSink float64

func benchmarkHaar1(b *testing.B, stride int) {
	const n0 = 32
	x := make([]float64, n0*stride)
	rng := rand.New(rand.NewSource(17 + int64(stride)))
	for i := range x {
		x[i] = rng.Float64()*2 - 1
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		haar1(x, n0, stride)
	}
	haar1BenchSink = x[len(x)-1]
}

func BenchmarkHaar1Stride1(b *testing.B) {
	benchmarkHaar1(b, 1)
}

func BenchmarkHaar1Stride2(b *testing.B) {
	benchmarkHaar1(b, 2)
}

func BenchmarkHaar1Stride4(b *testing.B) {
	benchmarkHaar1(b, 4)
}

func BenchmarkHaar1Stride6(b *testing.B) {
	benchmarkHaar1(b, 6)
}

func BenchmarkHaar1Stride8(b *testing.B) {
	benchmarkHaar1(b, 8)
}

func BenchmarkHaar1Stride12(b *testing.B) {
	benchmarkHaar1(b, 12)
}

func benchmarkHaar1Direct(b *testing.B, stride int, fn func([]float64, int)) {
	const n0 = 32
	x := make([]float64, n0*stride*2)
	rng := rand.New(rand.NewSource(97 + int64(stride)))
	for i := range x {
		x[i] = rng.Float64()*2 - 1
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn(x, n0)
	}
	haar1BenchSink = x[len(x)-1]
}

func BenchmarkHaar1Stride1Current(b *testing.B) {
	benchmarkHaar1Direct(b, 1, haar1Stride1Asm)
}

func BenchmarkHaar1Stride1Generic(b *testing.B) {
	benchmarkHaar1Direct(b, 1, haar1Stride1Generic)
}

func BenchmarkHaar1Stride2Current(b *testing.B) {
	benchmarkHaar1Direct(b, 2, haar1Stride2Asm)
}

func BenchmarkHaar1Stride2Generic(b *testing.B) {
	benchmarkHaar1Direct(b, 2, haar1Stride2Generic)
}

func BenchmarkHaar1Stride4Current(b *testing.B) {
	benchmarkHaar1Direct(b, 4, haar1Stride4Asm)
}

func BenchmarkHaar1Stride4Generic(b *testing.B) {
	benchmarkHaar1Direct(b, 4, haar1Stride4)
}
