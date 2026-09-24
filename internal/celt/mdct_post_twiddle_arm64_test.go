//go:build arm64 && !nosimd

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
			stageStorage := make([]kissCpx, n4+2)
			stageStorage[0] = kissCpx{123.5, -91.25}
			stageStorage[n4+1] = kissCpx{-65.75, 47.125}
			stage := stageStorage[1 : n4+1]
			for i := range stage {
				stage[i] = kissCpx{float32(rng.NormFloat64()), float32(rng.NormFloat64())}
			}
			trigStorage := make([]float32, n2+2)
			trigStorage[0] = 87.75
			trigStorage[n2+1] = -43.625
			trig := trigStorage[1 : n2+1]
			for i := range trig {
				trig[i] = float32(rng.NormFloat64())
			}
			pairBlocks := n4 >> 3
			if pairBlocks == 0 {
				continue
			}
			gotStorage := make([]float32, n2+2)
			gotStorage[0] = 31.875
			gotStorage[n2+1] = -27.125
			got := gotStorage[1 : n2+1]
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
			if stageStorage[0] != (kissCpx{123.5, -91.25}) || stageStorage[n4+1] != (kissCpx{-65.75, 47.125}) {
				t.Fatalf("n4=%d trial=%d: FFT input canary changed", n4, trial)
			}
			if trigStorage[0] != 87.75 || trigStorage[n2+1] != -43.625 {
				t.Fatalf("n4=%d trial=%d: trig input canary changed", n4, trial)
			}
			if gotStorage[0] != 31.875 || gotStorage[n2+1] != -27.125 {
				t.Fatalf("n4=%d trial=%d: coefficient output canary changed", n4, trial)
			}
		}
	}
}

func TestMDCTPostTwiddleNeonAllocs(t *testing.T) {
	const n4, n2 = 64, 128
	coeffs := make([]float32, n2)
	stage := make([]kissCpx, n4)
	trig := make([]float32, n2)
	for i := range stage {
		stage[i] = kissCpx{float32(i) * 0.03125, -float32(i) * 0.015625}
	}
	for i := range trig {
		trig[i] = float32(i) * 0.0078125
	}
	run := func() { mdctPostTwiddleNeon(coeffs, stage, trig, n2, n4, n4/8) }
	run()
	if allocs := testing.AllocsPerRun(100, run); allocs != 0 {
		t.Fatalf("post-twiddle allocated %v times per call, want 0", allocs)
	}
}
