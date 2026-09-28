//go:build gopus_dred && !gopus_qext && !gopus_fixed_point

package celt_test

import "testing"

const antiCollapseCheckFixtureQuality = false

func selectedAntiCollapseReferencePCM(_ *testing.T, fixture []float32, _ [][]byte, _, _ int) ([]float32, bool) {
	// The DRED-only build uses the pinned quality fixture until its paired
	// DRED-only C decoder helper is available here.
	return fixture, false
}
