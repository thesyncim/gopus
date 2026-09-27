package libopustest

const (
	celtExp2DBFixedQEXTInputMagic  = "GQEI"
	celtExp2DBFixedQEXTOutputMagic = "GQEO"
)

var celtExp2DBFixedQEXTHelper HelperCache

func buildCELTExp2DBFixedQEXTHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed QEXT CELT exp2_db",
		OutputBase:   "gopus_libopus_celt_exp2_db_fixed_qext",
		SourceFile:   "libopus_celt_exp2_db_fixed_qext_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeCELTExp2DBFixedQEXT returns the Q28 exp2_db fractional polynomial and
// Q16 full exponentiation from the selected FIXED_POINT+ENABLE_QEXT archive.
func ProbeCELTExp2DBFixedQEXT(values []int32) (frac, whole []int32, err error) {
	binPath, err := celtExp2DBFixedQEXTHelper.Path(buildCELTExp2DBFixedQEXTHelper)
	if err != nil {
		return nil, nil, err
	}
	payload := NewOraclePayload(celtExp2DBFixedQEXTInputMagic, uint32(len(values)))
	payload.I32s(values...)
	reader, err := RunOracle(binPath, payload.Bytes(), "fixed QEXT CELT exp2_db", celtExp2DBFixedQEXTOutputMagic)
	if err != nil {
		return nil, nil, err
	}
	count := reader.Count(len(values))
	reader.ExpectRemaining(8 * count)
	frac = make([]int32, count)
	whole = make([]int32, count)
	for i := range frac {
		frac[i] = reader.I32()
		whole[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return frac, whole, nil
}
