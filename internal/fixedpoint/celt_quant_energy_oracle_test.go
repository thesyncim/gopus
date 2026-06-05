//go:build gopus_fixed_point

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestAmp2Log2Oracle checks Amp2Log2 against the real libopus FIXED_POINT
// amp2Log2 bit-for-bit, over the standard 21-band CELT layout for mono and
// stereo, across band-amplitude sets that exercise EPSILON, small/large Q12
// magnitudes, every band index (so each eMeans entry is used), the effEnd<end
// tail fill, and a deterministic pseudo-random sweep.
func TestAmp2Log2Oracle(t *testing.T) {
	libopustest.RequireOracle(t)

	const nbEBands = 21

	type genFn func(total int) []int32
	gens := []struct {
		name string
		fn   genFn
	}{
		{"epsilon", func(total int) []int32 {
			x := make([]int32, total)
			for i := range x {
				x[i] = 1 // EPSILON
			}
			return x
		}},
		{"unit_q12", func(total int) []int32 {
			x := make([]int32, total)
			for i := range x {
				x[i] = 1 << 12
			}
			return x
		}},
		{"ramp", func(total int) []int32 {
			x := make([]int32, total)
			for i := range x {
				x[i] = int32((i + 1) << 8)
			}
			return x
		}},
		{"large", func(total int) []int32 {
			x := make([]int32, total)
			for i := range x {
				x[i] = int32((i + 1)) << 20
			}
			return x
		}},
		{"max_q12", func(total int) []int32 {
			x := make([]int32, total)
			for i := range x {
				x[i] = 1<<30 - 1
			}
			return x
		}},
		{"prng", func(total int) []int32 {
			x := make([]int32, total)
			state := uint32(0x12345677)
			for i := range x {
				state ^= state << 13
				state ^= state >> 17
				state ^= state << 5
				// Keep amplitudes positive and within a plausible Q12 range;
				// celt_log2 requires x > 0 (0 maps to its sentinel branch).
				v := int32(state >> uint(i%20))
				if v <= 0 {
					v = 1
				}
				x[i] = v
			}
			return x
		}},
	}

	type bounds struct{ effEnd, end int }
	cases := []bounds{
		{nbEBands, nbEBands},
		{nbEBands - 4, nbEBands},
		{10, 17},
		{0, nbEBands},
	}

	for _, C := range []int{1, 2} {
		total := C * nbEBands
		for _, b := range cases {
			for _, g := range gens {
				bandE := g.fn(total)

				want, err := libopustest.ProbeCELTFixedAmp2Log2(bandE, nbEBands, b.effEnd, b.end, C)
				if err != nil {
					libopustest.HelperUnavailable(t, "celt fixed amp2Log2", err)
					return
				}

				got := make([]int32, total)
				Amp2Log2(bandE, got, nbEBands, b.effEnd, b.end, C)

				if len(want) != len(got) {
					t.Fatalf("C=%d eff=%d end=%d %s: oracle returned %d logE, want %d",
						C, b.effEnd, b.end, g.name, len(want), len(got))
				}
				for i := range got {
					if got[i] != want[i] {
						t.Errorf("C=%d eff=%d end=%d %s: bandLogE[%d] = %d, libopus = %d",
							C, b.effEnd, b.end, g.name, i, got[i], want[i])
					}
				}
			}
		}
	}
}
