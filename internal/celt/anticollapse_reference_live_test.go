//go:build !gopus_dred || gopus_qext || gopus_fixed_point

package celt_test

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var antiCollapseSelectedRefHelper libopustest.HelperCache

// selectedAntiCollapseReferencePCM decodes the pinned packets through the
// libopus feature and instruction lane used by the active Go decoder.
func selectedAntiCollapseReferencePCM(t *testing.T, fixture []float32, packets [][]byte, frameSize, preSkip int) ([]float32, bool) {
	t.Helper()
	bin, err := antiCollapseSelectedRefHelper.Path(func() (string, error) {
		return libopustest.BuildCHelper(antiCollapseSelectedRefConfig())
	})
	if err != nil {
		t.Fatalf("build selected anti-collapse C decoder: %v", err)
	}
	return decodeAntiCollapseReferencePCM(t, bin, fixture, packets, frameSize, preSkip)
}
