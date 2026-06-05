//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestXcorrKernel4Float32NeonBitExact checks the fused NEON pitch kernel against
// the scalar reference bit-for-bit. The NEON kernel accumulates each lag with a
// single-rounding VFMLA; the scalar xcorrKernel4Float32 computes the same
// Sum x[i]*y[i+k] in the same per-lag order, and arm64 gc contracts its
// sum[k] += tmp*y into FMADDS — the identical IEEE single-rounding f32 FMA — so
// the two paths agree to the bit on every lane.
func TestXcorrKernel4Float32NeonBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for _, length := range []int{1, 2, 3, 4, 5, 7, 16, 17, 64, 240, 480, 481} {
		for trial := 0; trial < 50; trial++ {
			x := make([]float32, length)
			y := make([]float32, length+4)
			for i := range x {
				x[i] = float32(rng.NormFloat64())
			}
			for i := range y {
				y[i] = float32(rng.NormFloat64())
			}
			init := [4]float32{
				float32(rng.NormFloat64()),
				float32(rng.NormFloat64()),
				float32(rng.NormFloat64()),
				float32(rng.NormFloat64()),
			}
			got := init
			want := init
			xcorrKernel4Float32Neon(x, y, &got, length)
			xcorrKernel4Float32(x, y, &want, length)
			for k := 0; k < 4; k++ {
				if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
					t.Fatalf("len=%d trial=%d lane=%d: neon=%08x(%v) scalar=%08x(%v)",
						length, trial, k,
						math.Float32bits(got[k]), got[k],
						math.Float32bits(want[k]), want[k])
				}
			}
		}
	}
}

// BenchmarkXcorrKernel4F32Neon isolates the fused NEON 4-lag kernel for an A/B
// against BenchmarkXcorrKernel4F32 (scalar).
func BenchmarkXcorrKernel4F32Neon(b *testing.B) {
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
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sum = [4]float32{}
		xcorrKernel4Float32Neon(x, y, &sum, length)
	}
	_ = sum
}
