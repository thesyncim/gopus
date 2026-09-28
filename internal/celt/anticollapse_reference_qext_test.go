//go:build gopus_qext && !gopus_fixed_point && !gopus_dred

package celt_test

import "github.com/thesyncim/gopus/internal/libopustest"

func antiCollapseSelectedRefConfig() libopustest.CHelperConfig {
	return libopustest.CHelperConfig{
		Label:      "QEXT anti-collapse decode",
		OutputBase: "gopus_anticollapse_qext_decode",
		SourceFile: "libopus_qext_decode96k_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG", "-ffp-contract=off"},
		QEXTRef:    true,
		Libs:       []string{libopustest.QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	}
}
