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
