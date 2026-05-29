package libopustest

const (
	celtQuantEnergyInputMagic  = "GQEI"
	celtQuantEnergyOutputMagic = "GQEO"

	// CELTQuantEnergyModeAmp2Log2 selects the FIXED_POINT amp2Log2 kernel.
	CELTQuantEnergyModeAmp2Log2 = uint32(0)
)

var celtQuantEnergyHelper HelperCache

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

func getCELTQuantEnergyHelperPath() (string, error) {
	return celtQuantEnergyHelper.Path(buildCELTQuantEnergyHelper)
}

// ProbeCELTFixedAmp2Log2 runs the FIXED_POINT amp2Log2 kernel against the real
// libopus reference, returning the resulting log2 band energies (channel-major,
// Q24, length C*nbEBands). bandE is the channel-major Q12 band amplitude array of
// length C*nbEBands.
func ProbeCELTFixedAmp2Log2(bandE []int32, nbEBands, effEnd, end, C int) ([]int32, error) {
	binPath, err := getCELTQuantEnergyHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtQuantEnergyInputMagic, CELTQuantEnergyModeAmp2Log2, 0)
	payload.U32(uint32(nbEBands))
	payload.U32(uint32(effEnd))
	payload.U32(uint32(end))
	payload.U32(uint32(C))
	payload.I32s(bandE...)

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed amp2Log2", celtQuantEnergyOutputMagic)
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
