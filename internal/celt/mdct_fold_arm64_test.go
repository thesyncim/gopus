//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestMDCTFoldStoreNeonBitExact checks both windowed-fold NEON kernels
// against the scalar mix + store sequence bit-for-bit over the real MDCT
// geometries (overlap 120, limit1 30) and randomized block counts.
func TestMDCTFoldStoreNeonBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(43))
	const overlap = 120
	const limit1 = 30
	for _, n4 := range []int{30, 60, 120, 240} {
		n2 := 2 * n4
		samples := make([]float32, n2+overlap)
		for i := range samples {
			samples[i] = float32(rng.NormFloat64())
		}
		window := make([]float32, overlap)
		for i := range window {
			window[i] = rng.Float32()
		}
		trig := make([]float32, n2)
		for i := range trig {
			trig[i] = float32(rng.NormFloat64())
		}
		bitrev := rng.Perm(n4)
		preScale := float32(1.0) / float32(n4)

		for _, blocks := range []int{1, limit1 / 4} {
			// Leading fold geometry.
			i0 := 0
			xp1 := overlap / 2
			xp2 := n2 - 1 + overlap/2
			wp1 := overlap / 2
			wp2 := overlap/2 - 1
			got := make([]kissCpx, n4)
			want := make([]kissCpx, n4)
			for j := 0; j < 4*blocks; j++ {
				re := mdctMulAddMix(samples[xp1+n2+2*j], samples[xp2-2*j], window[wp2-2*j], window[wp1+2*j])
				im := mdctMulSubMix(samples[xp1+2*j], samples[xp2-n2-2*j], window[wp1+2*j], window[wp2-2*j])
				mdctStoreDirectStageFMALike(want, bitrev[i0+j], preScale, re, im, trig[i0+j], trig[n4+i0+j])
			}
			mdctFold1StoreNeon(got, bitrev, samples, window, trig, i0, n4, n2, xp1, xp2, wp1, wp2, blocks, preScale)
			compareCpx(t, "fold1", n4, blocks, got, want)

			// Trailing fold geometry.
			if n4 < 2*limit1 {
				continue
			}
			i0 = n4 - limit1
			xp1 = overlap/2 + 2*i0
			xp2 = n2 - 1 + overlap/2 - 2*i0
			wp1 = 0
			wp2 = overlap - 1
			got = make([]kissCpx, n4)
			want = make([]kissCpx, n4)
			for j := 0; j < 4*blocks; j++ {
				re := mdctMulSubMixAlt(samples[xp2-2*j], samples[xp1-n2+2*j], window[wp2-2*j], window[wp1+2*j])
				im := mdctMulAddMix(samples[xp1+2*j], samples[xp2+n2-2*j], window[wp2-2*j], window[wp1+2*j])
				mdctStoreDirectStageFMALike(want, bitrev[i0+j], preScale, re, im, trig[i0+j], trig[n4+i0+j])
			}
			mdctFold3StoreNeon(got, bitrev, samples, window, trig, i0, n4, n2, xp1, xp2, wp1, wp2, blocks, preScale)
			compareCpx(t, "fold3", n4, blocks, got, want)
		}
	}
}

func compareCpx(t *testing.T, name string, n4, blocks int, got, want []kissCpx) {
	t.Helper()
	for k := range want {
		if math.Float32bits(got[k].r) != math.Float32bits(want[k].r) ||
			math.Float32bits(got[k].i) != math.Float32bits(want[k].i) {
			t.Fatalf("%s n4=%d blocks=%d: dst[%d] = (%08x,%08x), want (%08x,%08x)",
				name, n4, blocks, k,
				math.Float32bits(got[k].r), math.Float32bits(got[k].i),
				math.Float32bits(want[k].r), math.Float32bits(want[k].i))
		}
	}
}
