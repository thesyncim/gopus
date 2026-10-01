//go:build amd64 && (!goexperiment.simd || nosimd || purego)

package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestSILKPitchXcorrMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := silkPitchXcorrLibopusCases()
	want, err := probeLibopusSILKPitchXcorrScalar(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "compiler-matched scalar silk pitch xcorr", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]float32, tc.maxPitch)
			celtPitchXcorrFloat(tc.x, tc.y, got, tc.length, tc.maxPitch)
			if len(got) != len(want[i]) {
				t.Fatalf("xcorr len=%d want %d", len(got), len(want[i]))
			}
			for j := range got {
				if math.Float32bits(got[j]) != math.Float32bits(want[i][j]) {
					t.Fatalf("xcorr[%d]=%08x %.10g want %08x %.10g",
						j, math.Float32bits(got[j]), got[j], math.Float32bits(want[i][j]), want[i][j])
				}
			}
		})
	}
}
