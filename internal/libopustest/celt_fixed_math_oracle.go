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
	// CELTFixedMathModeComputeBandEnergies selects the FIXED_POINT
	// compute_band_energies kernel (array-shaped oracle).
	CELTFixedMathModeComputeBandEnergies = uint32(3)
	// CELTFixedMathModeNormaliseBands selects the FIXED_POINT normalise_bands
	// kernel (array-shaped oracle).
	CELTFixedMathModeNormaliseBands = uint32(4)
	// CELTFixedMathModeRcp selects the FIXED_POINT celt_rcp kernel.
	CELTFixedMathModeRcp = uint32(5)
	// CELTFixedMathModeRcpNorm16 selects the FIXED_POINT celt_rcp_norm16 kernel.
	CELTFixedMathModeRcpNorm16 = uint32(6)
	// CELTFixedMathModeRcpNorm32 selects the FIXED_POINT celt_rcp_norm32 kernel.
	CELTFixedMathModeRcpNorm32 = uint32(7)
	// CELTFixedMathModeCosNorm selects the FIXED_POINT celt_cos_norm kernel.
	CELTFixedMathModeCosNorm = uint32(8)
	// CELTFixedMathModeCosNorm32 selects the FIXED_POINT celt_cos_norm32 kernel.
	CELTFixedMathModeCosNorm32 = uint32(9)
	// CELTFixedMathModeFracDiv32Q29 selects the FIXED_POINT frac_div32_q29
	// kernel (two int32 inputs per record).
	CELTFixedMathModeFracDiv32Q29 = uint32(10)
	// CELTFixedMathModeFracDiv32 selects the FIXED_POINT frac_div32 kernel
	// (two int32 inputs per record).
	CELTFixedMathModeFracDiv32 = uint32(11)
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

// ProbeCELTFixedMathPairs runs a two-input FIXED_POINT celt math kernel (the
// frac_div32 family) over a list of (a, b) pairs, returning one int32-as-uint32
// output per pair. The header record count is the number of pairs; each record
// consumes two int32 inputs.
func ProbeCELTFixedMathPairs(mode uint32, a, b []int32) ([]uint32, error) {
	if len(a) != len(b) {
		panic("ProbeCELTFixedMathPairs: a and b length mismatch")
	}
	binPath, err := getCELTFixedMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtFixedMathInputMagic, mode, uint32(len(a)))
	for i := range a {
		payload.I32(a[i])
		payload.I32(b[i])
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed math pairs", celtFixedMathOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(a))
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

// ProbeCELTFixedComputeBandEnergies runs the FIXED_POINT compute_band_energies
// kernel against the real libopus reference, returning the resulting band
// energies (channel-major, length C*nbEBands). The x slice is the
// channel-major frequency-domain signal of length C*(shortMdctSize<<LM).
func ProbeCELTFixedComputeBandEnergies(eBands, logN []int16, x []int32, nbEBands, shortMdctSize, end, C, LM int) ([]int32, error) {
	binPath, err := getCELTFixedMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtFixedMathInputMagic, CELTFixedMathModeComputeBandEnergies, 0)
	payload.U32(uint32(nbEBands))
	payload.U32(uint32(shortMdctSize))
	payload.U32(uint32(end))
	payload.U32(uint32(C))
	payload.U32(uint32(LM))
	for _, v := range eBands {
		payload.I32(int32(v))
	}
	for _, v := range logN {
		payload.I32(int32(v))
	}
	payload.I32s(x...)

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed compute_band_energies", celtFixedMathOutputMagic)
	if err != nil {
		return nil, err
	}
	want := C * nbEBands
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

// ProbeCELTFixedNormaliseBands runs the FIXED_POINT normalise_bands kernel
// against the real libopus reference, returning the normalised celt_norm output
// (channel-major, length C*N where N = M*shortMdctSize). The freq slice is the
// channel-major frequency-domain signal of the same length and bandE holds the
// per-band amplitudes (channel-major, length C*nbEBands).
func ProbeCELTFixedNormaliseBands(eBands []int16, bandE, freq []int32, nbEBands, shortMdctSize, end, C, M int) ([]int32, error) {
	binPath, err := getCELTFixedMathHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtFixedMathInputMagic, CELTFixedMathModeNormaliseBands, 0)
	payload.U32(uint32(nbEBands))
	payload.U32(uint32(shortMdctSize))
	payload.U32(uint32(end))
	payload.U32(uint32(C))
	payload.U32(uint32(M))
	for _, v := range eBands {
		payload.I32(int32(v))
	}
	payload.I32s(bandE...)
	payload.I32s(freq...)

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed normalise_bands", celtFixedMathOutputMagic)
	if err != nil {
		return nil, err
	}
	want := C * M * shortMdctSize
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
