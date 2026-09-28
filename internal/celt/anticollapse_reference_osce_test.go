//go:build gopus_osce && !gopus_qext && !gopus_fixed_point

package celt_test

import "testing"

const antiCollapseCheckFixtureQuality = true

func selectedAntiCollapseReferencePCM(t *testing.T, fixture []float32, packets [][]byte, frameSize, preSkip int) ([]float32, bool) {
	t.Helper()
	bin, err := buildAntiCollapseDNNReferenceHelper()
	if err != nil {
		t.Fatalf("build selected OSCE anti-collapse C decoder: %v", err)
	}
	return decodeAntiCollapseReferencePCM(t, bin, fixture, packets, frameSize, preSkip)
}
