//go:build gopus_fixed_point && gopus_qext

package multistream

import "github.com/thesyncim/gopus/internal/libopustest"

func fixedSurroundReferenceConfig() libopustest.CHelperConfig {
	return libopustest.CHelperConfig{FixedQEXTRef: true}
}

func fixedSurroundReferenceArchive() string {
	return libopustest.FixedQEXTRefPath(".libs", "libopus.a")
}

func fixedSurroundReferenceLabel() string {
	return "FIXED_POINT+ENABLE_QEXT runtime-off"
}
