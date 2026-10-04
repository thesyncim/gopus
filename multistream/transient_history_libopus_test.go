package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// Narrowband child encoders retain full mode-strided energy history across frames.
func TestSurroundTransientHistoryStrideMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, name := range []string{
		"quad/fs240/br32000/vbrfalse/cfalse/cx5",
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
