//go:build gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce

package celt_test

import "github.com/thesyncim/gopus/internal/libopustest"

func antiCollapseSelectedRefConfig() libopustest.CHelperConfig {
	return libopustest.CHelperConfig{
		Label:      "fixed anti-collapse decode",
		OutputBase: "gopus_anticollapse_fixed_decode",
		SourceFile: "libopus_qext_decode96k_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		FixedRef:   true,
		Libs:       []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	}
}
