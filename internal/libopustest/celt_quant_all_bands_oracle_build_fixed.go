//go:build !gopus_qext

package libopustest

func buildCELTQuantAllBandsHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt quant_all_bands fixed",
		OutputBase:  "gopus_libopus_celt_quant_all_bands_fixed",
		SourceFile:  "libopus_celt_quant_all_bands_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}
