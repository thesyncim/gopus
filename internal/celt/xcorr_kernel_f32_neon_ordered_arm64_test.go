//go:build arm64 && !nosimd && !purego

package celt

import (
	"math/rand"
	"testing"
)

var xcorrKernel4Float32NeonOrderedBenchmarkSink [4]float32

// BenchmarkXcorrKernel4F32NeonOrdered isolates the selected-C ordered kernel.
func BenchmarkXcorrKernel4F32NeonOrdered(b *testing.B) {
	const length = 480
	rng := rand.New(rand.NewSource(7))
	x := make([]float32, length)
	y := make([]float32, length+4)
	for i := range x {
		x[i] = float32(rng.NormFloat64())
	}
	for i := range y {
		y[i] = float32(rng.NormFloat64())
	}
	var sum [4]float32
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sum = [4]float32{}
		xcorrKernel4Float32NeonOrdered(x, y, &sum, length)
	}
	xcorrKernel4Float32NeonOrderedBenchmarkSink = sum
}
