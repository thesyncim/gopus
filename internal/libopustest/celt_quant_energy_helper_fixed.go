//go:build !gopus_qext

package libopustest

func buildCELTQuantEnergyHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt quant energy",
		OutputBase:  "gopus_libopus_celt_quant_energy",
		SourceFile:  "libopus_celt_quant_energy_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}
