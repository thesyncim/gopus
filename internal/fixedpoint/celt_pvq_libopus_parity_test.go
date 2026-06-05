//go:build gopus_fixed_point

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// pvqInputRanges spans the celt_norm magnitudes the search actually sees:
// post-rotation normalised residuals live around Q14 unit norm, but the
// internal norm_scaledown is driven by the full Q24 NORM_SHIFT scale, so we
// exercise both the small (already-int16) and large (needs scaledown) regimes.
type pvqRange struct {
	name string
	span int32
}

var pvqRanges = []pvqRange{
	{"q14", 1 << 14},
	{"q18", 1 << 18},
	{"norm", 1 << 24},
}

func randPVQInput(rng *rand.Rand, n int, span int32) []int32 {
	out := make([]int32, n)
	for i := range out {
		out[i] = rng.Int31n(2*span+1) - span
	}
	return out
}

func TestOpPvqSearchMatchesLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0x9817EBA))

	// (N, K) pairs covering: K small relative to N (pure greedy loop),
	// K > N/2 (pre-search projection path), N==2 minimum, and large bands.
	type tc struct{ n, k int }
	cases := []tc{
		{2, 1}, {2, 3}, {2, 8},
		{3, 1}, {3, 5}, {4, 2}, {4, 7},
		{8, 1}, {8, 4}, {8, 12}, {8, 40},
		{16, 3}, {16, 11}, {16, 32},
		{44, 7}, {44, 30},
		{120, 13}, {120, 200},
		{176, 24},
	}

	for _, c := range cases {
		for _, r := range pvqRanges {
			for trial := 0; trial < 8; trial++ {
				x := randPVQInput(rng, c.n, r.span)

				// The C kernel and the Go port both mutate X in place; give each
				// its own copy of the identical input.
				xC := append([]int32(nil), x...)
				xGo := append([]int32(nil), x...)

				wantYY, wantIY, err := libopustest.ProbeCELTPVQSearch(xC, c.k)
				if err != nil {
					libopustest.HelperUnavailable(t, "CELT fixed pvq", err)
				}

				iy := make([]int, c.n)
				gotYY := OpPvqSearch(xGo, iy, c.k, c.n, nil)

				if gotYY != wantYY {
					t.Fatalf("OpPvqSearch yy N=%d K=%d range=%s trial=%d got=%d want=%d",
						c.n, c.k, r.name, trial, gotYY, wantYY)
				}
				for i := 0; i < c.n; i++ {
					if int32(iy[i]) != wantIY[i] {
						t.Fatalf("OpPvqSearch iy[%d] N=%d K=%d range=%s trial=%d got=%d want=%d",
							i, c.n, c.k, r.name, trial, iy[i], wantIY[i])
					}
				}
			}
		}
	}
}

// TestOpPvqSearchSilencePathsMatchLibopusFixed pins the two degenerate branches
// the random fuzz rarely reaches: the K>N/2 pre-search "X too small" reset
// (sum<=K replaces X with a single Q14 pulse) and the pulsesLeft>N+3 dump that
// piles the leftover pulses into bin 0.
func TestOpPvqSearchSilencePathsMatchLibopusFixed(t *testing.T) {
	libopustest.RequireOracle(t)

	inputs := []struct {
		name string
		x    []int32
		k    int
	}{
		{"all_zero_large_k", []int32{0, 0, 0, 0}, 20},
		{"all_zero_small_k", []int32{0, 0, 0}, 2},
		{"tiny_large_k", []int32{1, -1, 2, -1, 1}, 50},
		{"one_spike", []int32{0, 1 << 20, 0, 0}, 9},
	}

	for _, in := range inputs {
		xC := append([]int32(nil), in.x...)
		xGo := append([]int32(nil), in.x...)

		wantYY, wantIY, err := libopustest.ProbeCELTPVQSearch(xC, in.k)
		if err != nil {
			libopustest.HelperUnavailable(t, "CELT fixed pvq", err)
		}

		iy := make([]int, len(in.x))
		gotYY := OpPvqSearch(xGo, iy, in.k, len(in.x), nil)

		if gotYY != wantYY {
			t.Fatalf("OpPvqSearch[%s] yy got=%d want=%d", in.name, gotYY, wantYY)
		}
		for i := range in.x {
			if int32(iy[i]) != wantIY[i] {
				t.Fatalf("OpPvqSearch[%s] iy[%d] got=%d want=%d", in.name, i, iy[i], wantIY[i])
			}
		}
	}
}
