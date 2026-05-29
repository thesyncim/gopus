package libopustest

const (
	celtDenormaliseInputMagic  = "GDBI"
	celtDenormaliseOutputMagic = "GDBO"
)

var celtDenormaliseHelper HelperCache

func buildCELTDenormaliseHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt denormalise_bands",
		OutputBase:  "gopus_libopus_celt_denormalise",
		SourceFile:  "libopus_celt_denormalise_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTDenormaliseHelperPath() (string, error) {
	return celtDenormaliseHelper.Path(buildCELTDenormaliseHelper)
}

// ProbeCELTFixedDenormaliseBands runs the FIXED_POINT denormalise_bands kernel
// against the real libopus reference, returning the de-normalised synthesis
// spectrum freq of length N = M*shortMdctSize.
//
//	eBands    mode band boundaries (m->eBands), length >= end+1
//	bandLogE  per-band quantized log energy (celt_glog), length >= end
//	x         normalized coefficients (celt_norm), length M*eBands[end]
func ProbeCELTFixedDenormaliseBands(eBands []int16, bandLogE, x []int32, nbEBands, shortMdctSize, start, end, M, downsample int, silence bool) ([]int32, error) {
	binPath, err := getCELTDenormaliseHelperPath()
	if err != nil {
		return nil, err
	}
	silenceWord := uint32(0)
	if silence {
		silenceWord = 1
	}
	payload := NewOraclePayload(celtDenormaliseInputMagic,
		uint32(nbEBands), uint32(shortMdctSize), uint32(start), uint32(end),
		uint32(M), uint32(downsample), silenceWord)
	for _, v := range eBands[:end+1] {
		payload.I32(int32(v))
	}
	for _, v := range bandLogE[:end] {
		payload.I32(v)
	}
	payload.I32s(x...)

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed denormalise_bands", celtDenormaliseOutputMagic)
	if err != nil {
		return nil, err
	}
	want := M * shortMdctSize
	count := reader.Count(want)
	reader.ExpectRemaining(4 * count)
	out := make([]int32, count)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
