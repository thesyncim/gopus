package libopustest

const (
	celtFixedMathInputMagic  = "GFMI"
	celtFixedMathOutputMagic = "GFMO"

	// CELTFixedMathModeSqrt selects the FIXED_POINT celt_sqrt kernel.
	CELTFixedMathModeSqrt = uint32(0)
	// CELTFixedMathModeSqrt32 selects the FIXED_POINT celt_sqrt32 kernel.
	CELTFixedMathModeSqrt32 = uint32(1)
	// CELTFixedMathModeRsqrtNorm32 selects the FIXED_POINT celt_rsqrt_norm32 kernel.
	CELTFixedMathModeRsqrtNorm32 = uint32(2)
)

var celtFixedMathHelper HelperCache

func buildCELTFixedMathHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt fixed math",
		OutputBase:  "gopus_libopus_celt_fixed_math",
		SourceFile:  "libopus_celt_fixed_math_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTFixedMathHelperPath() (string, error) {
	return celtFixedMathHelper.Path(buildCELTFixedMathHelper)
}

// ProbeCELTFixedMathWords runs the FIXED_POINT celt math oracle for the given
// mode over a list of int32-as-uint32 inputs, returning the int32-as-uint32
// outputs produced by libopus.
func ProbeCELTFixedMathWords(mode uint32, words []uint32) ([]uint32, error) {
	binPath, err := getCELTFixedMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtFixedMathInputMagic, mode, uint32(len(words)))
	for _, word := range words {
		payload.U32(word)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed math", celtFixedMathOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(words))
	reader.ExpectRemaining(4 * count)
	out := make([]uint32, count)
	for i := range out {
		out[i] = reader.U32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
