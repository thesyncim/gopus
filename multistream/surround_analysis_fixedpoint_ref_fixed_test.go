//go:build gopus_fixed_point && !gopus_qext

package multistream

import "github.com/thesyncim/gopus/internal/libopustest"

func fixedSurroundReferenceConfig() libopustest.CHelperConfig {
	return libopustest.CHelperConfig{FixedRef: true}
}

func fixedSurroundReferenceArchive() string {
	return libopustest.FixedRefPath(".libs", "libopus.a")
}

func fixedSurroundReferenceLabel() string {
	return "FIXED_POINT"
}
