//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestXcorrKernel4Float32Neon4AccBitExact checks the four-phase NEON pitch
// kernel against its order-matched scalar reference bit-for-bit: same
// per-phase FMA assignment, same (acc0+acc1)+(acc2+acc3) lane combination,
// same sequential tail.
func TestXcorrKernel4Float32Neon4AccBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for _, length := range []int{1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 64, 240, 480, 481, 718} {
		for trial := range 50 {
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
			xcorrKernel4Float32Neon4Acc(x, y, &got, length)
			xcorrKernel4Float32FourAccRef(x, y, &want, length)
			for k := range 4 {
				if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
					t.Fatalf("len=%d trial=%d lane=%d: neon=%08x(%v) ref=%08x(%v)",
						length, trial, k,
						math.Float32bits(got[k]), got[k],
						math.Float32bits(want[k]), want[k])
				}
			}
		}
	}
}

// BenchmarkXcorrKernel4F32Neon4Acc isolates the four-phase kernel for an A/B
// against BenchmarkXcorrKernel4F32Neon (single-chain).
func BenchmarkXcorrKernel4F32Neon4Acc(b *testing.B) {
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
		xcorrKernel4Float32Neon4Acc(x, y, &sum, length)
	}
	_ = sum
}
