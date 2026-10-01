//go:build gopus_qext && gopus_dred && !gopus_fixed_point && !gopus_osce

package celt_test

import "github.com/thesyncim/gopus/internal/libopustest"

func antiCollapseSelectedRefConfig() libopustest.CHelperConfig {
	return libopustest.CHelperConfig{
		Label:       "DRED-QEXT anti-collapse decode",
		OutputBase:  "gopus_anticollapse_dred_qext_decode",
		SourceFile:  "libopus_qext_decode96k_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		RefIncludes: []string{"dnn"},
		DREDQEXTRef: true,
		Libs:        []string{libopustest.DREDQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
}
