package libopustest

const (
	kissFFTInputMagic  = "GKFI"
	kissFFTOutputMagic = "GKFO"

	// KissFFTModeBfly2 selects the FIXED_POINT kf_bfly2 radix-2 butterfly.
	KissFFTModeBfly2 = uint32(0)
	// KissFFTModeBfly4 selects the FIXED_POINT kf_bfly4 radix-4 butterfly.
	KissFFTModeBfly4 = uint32(1)
	// KissFFTModeBfly3 selects the FIXED_POINT kf_bfly3 radix-3 butterfly.
	KissFFTModeBfly3 = uint32(2)
	// KissFFTModeBfly5 selects the FIXED_POINT kf_bfly5 radix-5 butterfly.
	KissFFTModeBfly5 = uint32(3)
)

// KissFFTTwiddle is a complex twiddle factor with int16 Q15 real/imaginary parts
// matching the FIXED_POINT (non-QEXT) libopus kiss_twiddle_cpx.
type KissFFTTwiddle struct {
	R int16
	I int16
}

// KissFFTRadixResult holds the libopus radix-3/4/5 butterfly oracle output: the
// twiddle table it generated for the requested nfft (so the Go side can consume
// identical Q15 twiddles) and the in-place transformed samples.
type KissFFTRadixResult struct {
	Twiddles []KissFFTTwiddle
	Samples  []KissFFTComplex
}

// ProbeKissFFTBflyRadix runs one of the FIXED_POINT radix-3/4/5 butterflies
// (selected by mode) over the given samples. The oracle rebuilds the twiddle
// table for nfft exactly as compute_twiddles() does, applies the static kernel
// body with the supplied fstride/m/N/mm strides, and returns both the twiddle
// table and the transformed samples.
func ProbeKissFFTBflyRadix(mode, nfft uint32, fstride, m, n, mm int, samples []KissFFTComplex) (KissFFTRadixResult, error) {
	binPath, err := getKissFFTHelperPath()
	if err != nil {
		return KissFFTRadixResult{}, err
	}
	payload := NewOraclePayload(kissFFTInputMagic, mode, nfft)
	payload.U32(uint32(fstride))
	payload.U32(uint32(m))
	payload.U32(uint32(n))
	payload.U32(uint32(mm))
	payload.U32(uint32(len(samples)))
	for _, s := range samples {
		payload.I32(s.R)
		payload.I32(s.I)
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "kiss fft", kissFFTOutputMagic)
	if err != nil {
		return KissFFTRadixResult{}, err
	}
	gotNfft := int(reader.U32())
	total := reader.Count(len(samples))
	reader.ExpectRemaining(8 * (gotNfft + total))
	res := KissFFTRadixResult{
		Twiddles: make([]KissFFTTwiddle, gotNfft),
		Samples:  make([]KissFFTComplex, total),
	}
	for i := range res.Twiddles {
		res.Twiddles[i].R = int16(reader.I32())
		res.Twiddles[i].I = int16(reader.I32())
	}
	for i := range res.Samples {
		res.Samples[i].R = reader.I32()
		res.Samples[i].I = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return KissFFTRadixResult{}, err
	}
	return res, nil
}

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
