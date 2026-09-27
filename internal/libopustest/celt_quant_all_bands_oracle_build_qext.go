//go:build gopus_qext

package libopustest

func buildCELTQuantAllBandsHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "celt quant_all_bands fixed qext",
		OutputBase:   "gopus_libopus_celt_quant_all_bands_fixed_qext",
		SourceFile:   "libopus_celt_quant_all_bands_fixed_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}
