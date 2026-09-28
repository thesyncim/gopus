//go:build gopus_fixed_point && gopus_dred

package celt_test

import "github.com/thesyncim/gopus/internal/libopustest"

func antiCollapseSelectedRefConfig() libopustest.CHelperConfig {
	// BuildCHelper validates DRED-QEXT pairing before compiling or linking.
	// Pinned libopus has no FIXED_POINT + ENABLE_DRED reference archive.
	return libopustest.CHelperConfig{
		Label:       "fixed-DRED anti-collapse decode",
		OutputBase:  "gopus_anticollapse_fixed_dred_decode",
		SourceFile:  "libopus_qext_decode96k_info.c",
		DREDQEXTRef: true,
	}
}
