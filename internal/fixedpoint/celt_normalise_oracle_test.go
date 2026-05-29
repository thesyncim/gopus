//go:build gopus_fixedpoint

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestNormaliseBandsOracle checks NormaliseBands against the real libopus
// FIXED_POINT normalise_bands bit-for-bit, across the standard 960-sample MDCT
// mode at every short-block multiplier (M = 1,2,4,8) for mono and stereo, over
// band energies that exercise the very-low-energy EPSILON guard, the celt_zlog2
// shift range, and a deterministic pseudo-random signal sweep.
func TestNormaliseBandsOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		nbEBands      = 21
		shortMdctSize = 120
		end           = 21
	)

	// xorshift32 PRNG for deterministic, reproducible inputs.
	next := func(state *uint32) uint32 {
		s := *state
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		*state = s
		return s
	}

	for lm := 0; lm <= 3; lm++ {
		M := 1 << lm
		n := M * shortMdctSize
		for _, C := range []int{1, 2} {
			state := uint32(0x12345 + lm*7 + C*131)

			freq := make([]int32, C*n)
			for i := range freq {
				// Wide signed magnitudes, including small and large values.
				v := int32(next(&state))
				freq[i] = v >> uint(i%20)
			}

			bandE := make([]int32, C*nbEBands)
			for i := range bandE {
				switch i % 6 {
				case 0:
					bandE[i] = 1 // below the EPSILON guard threshold
				case 1:
					bandE[i] = 9
				case 2:
					bandE[i] = 10
				default:
					// Spread positive magnitudes across the zlog2 range.
					bandE[i] = int32(next(&state)>>1)>>uint(i%22) | 1
				}
			}

			want, err := libopustest.ProbeCELTFixedNormaliseBands(
				testEband5ms, bandE, freq, nbEBands, shortMdctSize, end, C, M)
			if err != nil {
				libopustest.HelperUnavailable(t, "celt fixed normalise_bands", err)
				return
			}

			got := make([]int32, C*n)
			NormaliseBands(freq, got, bandE, testEband5ms, nbEBands, shortMdctSize, end, C, M)

			if len(want) != len(got) {
				t.Fatalf("LM=%d C=%d: oracle returned %d samples, want %d", lm, C, len(want), len(got))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("LM=%d C=%d: X[%d] = %d, libopus = %d", lm, C, i, got[i], want[i])
				}
			}
		}
	}
}
