//go:build gopus_qext

package libopustest

func buildCELTDenormaliseHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "celt fixed QEXT denormalise_bands",
		OutputBase:   "gopus_libopus_celt_denormalise_fixed_qext",
		SourceFile:   "libopus_celt_denormalise_fixed_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}
