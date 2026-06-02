//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestXcorrKernel4Float32NeonCloseToScalar checks the fused NEON pitch kernel
// against the scalar reference. FMA single-rounding diverges from
// multiply-then-add by at most ~1 ULP per term; over a long accumulation the
// relative error stays far below the pitch-search decision granularity.
func TestXcorrKernel4Float32NeonCloseToScalar(t *testing.T) {
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
				diff := math.Abs(float64(got[k]) - float64(want[k]))
				scale := math.Max(1, math.Abs(float64(want[k])))
				if diff/scale > 1e-4 {
					t.Fatalf("len=%d trial=%d lane=%d: neon=%v scalar=%v reldiff=%g",
						length, trial, k, got[k], want[k], diff/scale)
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
