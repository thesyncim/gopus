package multistream

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestShortFrameCompositeSilenceMatchesLibopus exercises the CELT silence
// decision on 2.5 and 5 ms frames. libopus celt_encoder.c uses the coded
// channel count for its raw-input max scan while pre-emphasizing the physical
// channel count; these streams include stereo input with a mono-coded child.
func TestShortFrameCompositeSilenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, name := range []string{
		"stereo/fs120/br32000/vbrfalse/cfalse/cx0",
		"stereo/fs240/br32000/vbrtrue/ctrue/cx5",
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

	for _, name := range []string{
		"foa-4ch/fs120/br64000/vbrfalse/cfalse/fmt0",
		"soa-9ch/fs240/br64000/vbrfalse/cfalse/fmt0",
	} {
		t.Run(name, func(t *testing.T) {
			var spec projectionFuzzSpec
			for _, candidate := range buildProjectionFuzzSweep() {
				if candidate.name == name {
					spec = candidate
					break
				}
			}
			if spec.seed == 0 {
				t.Fatal("projection matrix case missing")
			}
			runProjectionFuzzSpecParity(t, spec, true)
		})
	}
}
