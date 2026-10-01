//go:build amd64.v3 && !gopus_fixed_point

package silk

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestSILKWarpedAutocorrelationNativeV3MatchesLinkedLibopus exercises the
// paired native v3 libopus archive on deterministic inputs that emphasize
// cancellation and different SILK shaping orders. The oracle compares the
// final float32 correlation bits; it does not relax the kernel parity gate.
func TestSILKWarpedAutocorrelationNativeV3MatchesLinkedLibopus(t *testing.T) {
	requireLibopusAMD64V3Target(t)
	libopustest.RequireOracle(t)
	const caseCount = 640
	orders := [...]int{12, 14, 16, 20, 24}
	lengths := [...]int{48, 72, 96, 120, 160, 192, 240}
	scales := [...]float32{0.03125, 0.3, 1, 4096}
	rng := rand.New(rand.NewSource(0x6f8e9d1029384756))
	cases := make([]libopusSILKWarpedAutocorrCase, caseCount)
	for trial := range cases {
		order := orders[trial%len(orders)]
		length := lengths[(trial/len(orders))%len(lengths)]
		if length <= order {
			length = 48
		}
		scale := scales[(trial/17)%len(scales)]
		warping := float32(int32(rng.Intn(32769)-16384)) / 32768
		x := make([]float32, length)
		for i := range x {
			x[i] = (rng.Float32()*2 - 1) * scale
		}
		// A short alternating tail drives cancellation in higher-order
		// correlations without changing the source-valid float32 domain.
		if length >= 16 && trial%4 == 0 {
			for i := length - 16; i < length; i++ {
				x[i] = scale * float32(1-2*(i&1))
			}
		}
		cases[trial] = libopusSILKWarpedAutocorrCase{
			name:    "v3_case",
			order:   order,
			warping: warping,
			x:       x,
		}
	}

	want, err := probeLibopusSILKWarpedAutocorr(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "native v3 silk warped autocorrelation", err)
	}
	for i, tc := range cases {
		out := make([]float32, tc.order+1)
		warpedAutocorrelationFLP32(out, nil, tc.x, tc.warping, len(tc.x), tc.order)
		for j := range out {
			if gotBits, wantBits := math.Float32bits(out[j]), math.Float32bits(want[i][j]); gotBits != wantBits {
				t.Fatalf("case=%d order=%d length=%d warping=%08x corr[%d]=%08x %.10g want=%08x %.10g",
					i, tc.order, len(tc.x), math.Float32bits(tc.warping), j,
					gotBits, out[j], wantBits, want[i][j])
			}
		}
	}
}
