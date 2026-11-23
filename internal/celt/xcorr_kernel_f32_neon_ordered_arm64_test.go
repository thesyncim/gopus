//go:build arm64 && !nosimd && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

var (
	xcorrKernel4Float32NeonOrderedBenchmarkSink  [4]float32
	xcorrKernel16Float32NeonOrderedBenchmarkSink [16]float32
)

func TestXcorrKernel16F32NeonOrderedBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for _, length := range []int{0, 1, 7, 64, 240, 480} {
		x := make([]float32, max(length, 1))
		y := make([]float32, length+16)
		for i := range x {
			x[i] = float32(rng.NormFloat64())
		}
		for i := range y {
			y[i] = float32(rng.NormFloat64())
		}
		var got [16]float32
		xcorrKernel16Float32NeonOrdered(x, y, &got, length)
		for k := 0; k < 16; k += 4 {
			var want [4]float32
			xcorrKernel4Float32NeonOrdered(x, y[k:], &want, length)
			for j := range 4 {
				if math.Float32bits(got[k+j]) != math.Float32bits(want[j]) {
					t.Fatalf("len=%d lag=%d: got %08x want %08x", length, k+j, math.Float32bits(got[k+j]), math.Float32bits(want[j]))
				}
			}
		}
	}
}

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

// BenchmarkXcorrKernel16F32NeonOrdered benchmarks the 16-lag (4x4) ordered kernel.
func BenchmarkXcorrKernel16F32NeonOrdered(b *testing.B) {
	const length = 480
	rng := rand.New(rand.NewSource(7))
	x := make([]float32, length)
	y := make([]float32, length+16)
	for i := range x {
		x[i] = float32(rng.NormFloat64())
	}
	for i := range y {
		y[i] = float32(rng.NormFloat64())
	}
	var dst [16]float32
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		xcorrKernel16Float32NeonOrdered(x, y, &dst, length)
	}
	xcorrKernel16Float32NeonOrderedBenchmarkSink = dst
}
