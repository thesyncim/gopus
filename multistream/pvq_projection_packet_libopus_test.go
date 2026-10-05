package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// These live packets cross PVQ projection rounding boundaries in coded bands.
func TestSurroundPVQProjectionRoundingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, name := range []string{
		"mono/fs120/br256000/vbrtrue/cfalse/cx10",
		"quad/fs1920/br128000/vbrtrue/ctrue/cx0",
		"surround_5_1/fs2880/br510000/vbrtrue/cfalse/cx5",
		"surround_7_1/fs2880/br510000/vbrfalse/cfalse/cx10",
	} {
		t.Run(name, func(t *testing.T) {
			var spec surroundFuzzSpec
			for _, candidate := range buildSurroundFuzzSweep() {
				if candidate.name == name {
					spec = candidate
					break
				}
			}
			if spec.seed == 0 {
				t.Fatal("surround matrix case missing")
			}
			runSurroundFuzzSpecParity(t, spec, true)
		})
	}

}
