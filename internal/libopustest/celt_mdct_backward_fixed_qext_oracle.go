//go:build gopus_fixed_point && gopus_qext

package libopustest

import "fmt"

type CELTFixedQEXTMDCTBackwardParams struct {
	Mode, Shift, Stride int
	Input, Initial      []int32
}

var celtFixedQEXTMDCTBackwardHelper HelperCache

func buildCELTFixedQEXTMDCTBackwardHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT CELT inverse MDCT",
		OutputBase:   "gopus_libopus_celt_mdct_backward_fixed_qext",
		SourceFile:   "libopus_celt_mdct_backward_fixed_qext_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeCELTFixedQEXTMDCTBackward invokes the selected linked libopus
// clt_mdct_backward_c implementation for the supplied Q31 spectrum.
func ProbeCELTFixedQEXTMDCTBackward(p CELTFixedQEXTMDCTBackwardParams) ([]int32, error) {
	var n, overlap int
	switch p.Mode {
	case 0:
		n, overlap = 1920, 120
	case 1:
		n, overlap = 3840, 240
	default:
		return nil, fmt.Errorf("invalid fixed-QEXT mode %d", p.Mode)
	}
	if p.Shift < 0 || p.Shift > 3 || p.Stride < 1 || p.Stride > 8 {
		return nil, fmt.Errorf("invalid fixed-QEXT inverse MDCT dimensions")
	}
	wantIn := p.Stride*((n>>p.Shift>>1)-1) + 1
	if len(p.Input) != wantIn {
		return nil, fmt.Errorf("fixed-QEXT inverse MDCT input length %d, want %d", len(p.Input), wantIn)
	}
	outCount := (n >> p.Shift >> 1) + overlap/2
	if len(p.Initial) != outCount {
		return nil, fmt.Errorf("fixed-QEXT inverse MDCT initial output length %d, want %d", len(p.Initial), outCount)
	}
	payload := NewOraclePayloadVersion("GQBI", 1, uint32(p.Mode), uint32(p.Shift), uint32(p.Stride), uint32(len(p.Input)), uint32(len(p.Initial)))
	payload.I32s(p.Input...)
	payload.I32s(p.Initial...)
	bin, err := celtFixedQEXTMDCTBackwardHelper.Path(buildCELTFixedQEXTMDCTBackwardHelper)
	if err != nil {
		return nil, err
	}
	reader, err := RunOracle(bin, payload.Bytes(), "fixed-QEXT CELT inverse MDCT", "GQBO")
	if err != nil {
		return nil, err
	}
	if got := int(reader.U32()); got != outCount {
		return nil, fmt.Errorf("fixed-QEXT inverse MDCT output count %d, want %d", got, outCount)
	}
	out := make([]int32, outCount)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
