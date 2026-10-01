package libopustest

func celtDecodeHelperConfig() CHelperConfig {
	return CHelperConfig{
		Label:       "celt decode fixed",
		OutputBase:  "gopus_libopus_celt_decode_fixed",
		SourceFile:  "libopus_celt_decode_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
}

func celtSynthesisHelperConfig() CHelperConfig {
	return CHelperConfig{
		Label:       "celt synthesis fixed",
		OutputBase:  "gopus_libopus_celt_synthesis_fixed",
		SourceFile:  "libopus_celt_synthesis_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	}
}
