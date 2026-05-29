package libopustest

const (
	kissFFTInputMagic  = "GKFI"
	kissFFTOutputMagic = "GKFO"

	// KissFFTModeBfly2 selects the FIXED_POINT kf_bfly2 radix-2 butterfly.
	KissFFTModeBfly2 = uint32(0)
)

var kissFFTHelper HelperCache

func buildKissFFTHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "kiss fft",
		OutputBase:  "gopus_libopus_kiss_fft",
		SourceFile:  "libopus_kiss_fft_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{"-lm"},
		DeadStrip:   true,
	})
}

func getKissFFTHelperPath() (string, error) {
	return kissFFTHelper.Path(buildKissFFTHelper)
}

// KissFFTComplex is a complex sample with int32 real/imaginary parts matching
// the FIXED_POINT (non-QEXT) libopus kiss_fft_cpx.
type KissFFTComplex struct {
	R int32
	I int32
}

// ProbeKissFFTBfly2 runs the FIXED_POINT kf_bfly2 butterfly over the given
// samples (which must be a multiple of 8 in length: 8 complex samples per
// group) and returns the in-place transformed result produced by libopus.
func ProbeKissFFTBfly2(samples []KissFFTComplex) ([]KissFFTComplex, error) {
	if len(samples)%8 != 0 {
		panic("ProbeKissFFTBfly2: sample count must be a multiple of 8")
	}
	binPath, err := getKissFFTHelperPath()
	if err != nil {
		return nil, err
	}
	groups := uint32(len(samples) / 8)
	payload := NewOraclePayload(kissFFTInputMagic, KissFFTModeBfly2, groups)
	for _, s := range samples {
		payload.I32(s.R)
		payload.I32(s.I)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "kiss fft", kissFFTOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(samples))
	reader.ExpectRemaining(8 * count)
	out := make([]KissFFTComplex, count)
	for i := range out {
		out[i].R = reader.I32()
		out[i].I = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
