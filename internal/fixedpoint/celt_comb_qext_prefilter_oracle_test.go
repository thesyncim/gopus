//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestCombFilterQEXTPFMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	rng := rand.New(rand.NewSource(0x51455854))
	tests := []struct {
		t0, t1           int
		g0, g1           int16
		tapset0, tapset1 int
		n, overlap       int
	}{
		{t0: 64, t1: 92, g0: -12000, g1: -9000, tapset0: 1, tapset1: 2, n: 480, overlap: 120},
		{t0: 80, t1: 80, g0: -15000, g1: -15000, tapset0: 0, tapset1: 0, n: 960, overlap: 120},
		{t0: 40, t1: 73, g0: -11000, g1: 0, tapset0: 2, tapset1: 1, n: 240, overlap: 120},
	}
	for caseIndex, tc := range tests {
		t.Run(string(rune('a'+caseIndex)), func(t *testing.T) {
			yOffset, xOffset := 17, 320
			y := make([]int32, yOffset+tc.n+8)
			x := make([]int32, xOffset+tc.n+8)
			for i := range y {
				y[i] = int32(rng.Intn(1<<23) - (1 << 22))
			}
			for i := range x {
				x[i] = int32(rng.Intn(1<<23) - (1 << 22))
			}
			window := staticQEXTMDCT48000Window[:tc.overlap]
			params := libopustest.CELTFixedQEXTCombParams{
				Y: y, YOffset: yOffset, X: x, XOffset: xOffset, N: tc.n,
				T0: tc.t0, T1: tc.t1, G0: tc.g0, G1: tc.g1,
				Tapset0: tc.tapset0, Tapset1: tc.tapset1, Window: window, Overlap: tc.overlap,
			}
			want, err := libopustest.ProbeCELTFixedQEXTComb(params)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT CELT comb prefilter", err)
			}
			got := append([]int32(nil), y...)
			CombFilterQEXTPF(got, yOffset, x, xOffset, tc.t0, tc.t1, tc.n,
				tc.g0, tc.g1, tc.tapset0, tc.tapset1, window, tc.overlap)
			for i := range want {
				if got[yOffset+i] != want[i] {
					t.Fatalf("output[%d]=%d want %d (t0=%d t1=%d g0=%d g1=%d taps=%d/%d N=%d overlap=%d)",
						i, got[yOffset+i], want[i], tc.t0, tc.t1, tc.g0, tc.g1,
						tc.tapset0, tc.tapset1, tc.n, tc.overlap)
				}
			}
		})
	}
}
