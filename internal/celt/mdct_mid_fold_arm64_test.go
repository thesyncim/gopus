//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestMDCTMidFoldStoreNeonBitExact checks the NEON middle-fold kernel against
// the scalar mdctStoreDirectStageFMALike sequence bit-for-bit over the real
// mode geometries (n4 of 30/60/120/240 with overlap 120) and randomized
// off-grid shapes.
func TestMDCTMidFoldStoreNeonBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(41))
	type shape struct {
		n4, i0, xp1, xp2, blocks int
	}
	var shapes []shape
	// Real geometries: i0 = limit1, xp1 = overlap/2 + 2*limit1,
	// xp2 = n2-1+overlap/2 - 2*limit1, overlap=120, limit1=30.
	for _, n4 := range []int{60, 120, 240} {
		mid := n4 - 2*30
		if mid < 4 {
			continue
		}
		shapes = append(shapes, shape{
			n4: n4, i0: 30, xp1: 60 + 60, xp2: 2*n4 - 1 + 60 - 60, blocks: mid >> 2,
		})
	}
	// Off-grid shapes exercising remainder-adjacent block counts.
	for range 20 {
		n4 := 8 + rng.Intn(64)*4
		blocks := 1 + rng.Intn(n4/4)
		i0 := rng.Intn(n4 - 4*blocks + 1)
		xp1 := rng.Intn(8)
		xp2 := 8*blocks - 2 + rng.Intn(16) // ensures xp2-2*done+2 >= 0
		shapes = append(shapes, shape{n4: n4, i0: i0, xp1: xp1, xp2: xp2, blocks: blocks})
	}

	for si, s := range shapes {
		done := 4 * s.blocks
		need := s.xp1 + 2*done
		if low := s.xp2 + 2; low > need {
			need = low
		}
		samples := make([]float32, need)
		for i := range samples {
			samples[i] = float32(rng.NormFloat64())
		}
		trig := make([]float32, 2*s.n4)
		for i := range trig {
			trig[i] = float32(rng.NormFloat64())
		}
		bitrev := make([]int, s.n4)
		perm := rng.Perm(s.n4)
		copy(bitrev, perm)
		preScale := float32(1.0) / float32(s.n4)

		got := make([]kissCpx, s.n4)
		want := make([]kissCpx, s.n4)
		for j := range done {
			re := samples[s.xp2-2*j]
			im := samples[s.xp1+2*j]
			mdctStoreDirectStageFMALike(want, bitrev[s.i0+j], preScale, re, im, trig[s.i0+j], trig[s.n4+s.i0+j])
		}
		mdctMidFoldStoreNeon(got, bitrev, samples, trig, s.i0, s.n4, s.xp1, s.xp2, s.blocks, preScale)

		for k := range want {
			if math.Float32bits(got[k].r) != math.Float32bits(want[k].r) ||
				math.Float32bits(got[k].i) != math.Float32bits(want[k].i) {
				t.Fatalf("shape %d (%+v): dst[%d] = (%08x,%08x), want (%08x,%08x)",
					si, s, k,
					math.Float32bits(got[k].r), math.Float32bits(got[k].i),
					math.Float32bits(want[k].r), math.Float32bits(want[k].i))
			}
		}
	}
}
