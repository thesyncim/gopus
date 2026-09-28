package libopustest

func buildCELTQuantAllBandsQ15Helper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt quant_all_bands fixed Q15",
		OutputBase:  "gopus_libopus_celt_quant_all_bands_fixed_q15",
		SourceFile:  "libopus_celt_quant_all_bands_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}
