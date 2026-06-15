//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestMDCTPostTwiddleNeonBitExact pins the mirror-pair NEON post-twiddle to
// the scalar loop bit-for-bit across the production n4 sizes and odd block
// counts.
func TestMDCTPostTwiddleNeonBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(37))
	for _, n4 := range []int{8, 16, 24, 30, 60, 120, 240} {
		n2 := 2 * n4
		for trial := range 6 {
			stage := make([]kissCpx, n4)
			for i := range stage {
				stage[i] = kissCpx{float32(rng.NormFloat64()), float32(rng.NormFloat64())}
			}
			trig := make([]float32, n2)
			for i := range trig {
				trig[i] = float32(rng.NormFloat64())
			}
			pairBlocks := n4 >> 3
			if pairBlocks == 0 {
				continue
			}
			got := make([]float32, n2)
			want := make([]float32, n2)
			for i := 0; i < 4*pairBlocks; i++ {
				j := n4 - 1 - i
				want[2*i] = mdctMul(stage[i].i, trig[n4+i]) - mdctMul(stage[i].r, trig[i])
				want[n2-1-2*i] = mdctMul(stage[i].r, trig[n4+i]) + mdctMul(stage[i].i, trig[i])
				want[2*j] = mdctMul(stage[j].i, trig[n4+j]) - mdctMul(stage[j].r, trig[j])
				want[n2-1-2*j] = mdctMul(stage[j].r, trig[n4+j]) + mdctMul(stage[j].i, trig[j])
			}
			mdctPostTwiddleNeon(got, stage, trig, n2, n4, pairBlocks)
			for k := range want {
				if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
					t.Fatalf("n4=%d trial=%d: coeffs[%d] = %08x, want %08x", n4, trial, k,
						math.Float32bits(got[k]), math.Float32bits(want[k]))
				}
			}
		}
	}
}
