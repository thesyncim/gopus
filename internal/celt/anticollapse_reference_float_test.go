//go:build !gopus_dred && !gopus_qext && !gopus_fixed_point && !gopus_osce

package celt_test

import "github.com/thesyncim/gopus/internal/libopustest"

const antiCollapseCheckFixtureQuality = true

func antiCollapseSelectedRefConfig() libopustest.CHelperConfig {
	return libopustest.CHelperConfig{
		Label:      "float anti-collapse decode",
		OutputBase: "gopus_anticollapse_float_decode",
		SourceFile: "libopus_qext_decode96k_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	}
}
