package libopustest

const (
	celtMDCTInputMagic  = "GCMI"
	celtMDCTOutputMagic = "GCMO"

	// CELTMDCTModeForward selects the FIXED_POINT clt_mdct_forward_c transform.
	CELTMDCTModeForward = uint32(0)
	// CELTMDCTModeBackward selects the FIXED_POINT clt_mdct_backward_c transform.
	CELTMDCTModeBackward = uint32(1)
)

// CELTMDCTParams describes one MDCT oracle invocation: the full MDCT length n
// and the lookup's maxshift used to build the kfft/trig tables, plus the
// per-call shift/overlap/stride and the int16 Q15 window (overlap entries).
type CELTMDCTParams struct {
	N        int
	MaxShift int
	Shift    int
	Overlap  int
	Stride   int
	Window   []int16
}

// ProbeCELTMDCT drives the real libopus clt_mdct_forward_c (forward) or
// clt_mdct_backward_c (backward) against a reconstructed FIXED_POINT (non-QEXT)
// mdct_lookup for the given parameters, returning the int32 output buffer.
//
// For the forward transform in holds N reals and the output is the post-rotated
// spectrum of stride*(N2-1)+1 reals (N2 = (N>>shift)>>1). For the backward
// transform in holds stride*(N2-1)+1 frequency reals and the output is the N
// time-domain reals after windowing/mirroring.
func ProbeCELTMDCT(mode uint32, p CELTMDCTParams, in []int32) ([]int32, error) {
	binPath, err := getCELTMDCTHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtMDCTInputMagic, mode, uint32(p.N))
	payload.U32(uint32(p.MaxShift))
	payload.U32(uint32(p.Shift))
	payload.U32(uint32(p.Overlap))
	payload.U32(uint32(p.Stride))
	for _, v := range in {
		payload.I32(v)
	}
	for _, w := range p.Window {
		payload.U32(uint32(uint16(w)))
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt mdct", celtMDCTOutputMagic)
	if err != nil {
		return nil, err
	}
	outCount := int(reader.U32())
	reader.ExpectRemaining(4 * outCount)
	out := make([]int32, outCount)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

var celtMDCTHelper HelperCache

func buildCELTMDCTHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt mdct",
		OutputBase:  "gopus_libopus_celt_mdct_fixed",
		SourceFile:  "libopus_celt_mdct_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTMDCTHelperPath() (string, error) {
	return celtMDCTHelper.Path(buildCELTMDCTHelper)
}
