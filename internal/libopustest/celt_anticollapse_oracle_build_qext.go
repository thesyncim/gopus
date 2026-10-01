//go:build gopus_qext

package libopustest

func buildCELTAntiCollapseHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "CELT fixed QEXT anti-collapse",
		OutputBase:   "gopus_libopus_celt_anticollapse_fixed_qext",
		SourceFile:   "libopus_celt_anticollapse_fixed_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}
