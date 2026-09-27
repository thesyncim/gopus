//go:build gopus_qext

package libopustest

import "fmt"

type CELTFixedQEXTCubicParams struct {
	N, Resolution, Blocks int
	Gain                  int32
	Coded                 []byte
}

type CELTFixedQEXTCubicResult struct {
	Collapse              uint32
	Range, Val            uint32
	Tell, TellFrac, Error uint32
	Samples               []int32
}

var fixedQEXTCubicHelper HelperCache

func buildCELTFixedQEXTCubicHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT CELT cubic decoder oracle",
		OutputBase:   "gopus_libopus_celt_cubic_unquant_fixed_qext",
		SourceFile:   "libopus_celt_cubic_unquant_fixed_qext_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeCELTFixedQEXTCubic compares fixed-QEXT cubic_unquant with the supplied
// extension-coder bytes, including its returned collapse mask and coder state.
func ProbeCELTFixedQEXTCubic(p CELTFixedQEXTCubicParams) (CELTFixedQEXTCubicResult, error) {
	var out CELTFixedQEXTCubicResult
	if p.N < 1 || p.N > 512 || p.Resolution < 0 || p.Resolution > 14 ||
		p.Blocks < 1 || p.Blocks > 16 || len(p.Coded) > 4096 {
		return out, fmt.Errorf("invalid fixed-QEXT cubic controls")
	}
	bin, err := fixedQEXTCubicHelper.Path(buildCELTFixedQEXTCubicHelper)
	if err != nil {
		return out, err
	}
	payload := NewOraclePayloadVersion("GQCI", 1, uint32(p.N), uint32(p.Resolution), uint32(p.Blocks), uint32(p.Gain), uint32(len(p.Coded)))
	payload.Raw(p.Coded)
	if pad := (4 - len(p.Coded)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}
	reader, err := RunOracle(bin, payload.Bytes(), "fixed-QEXT CELT cubic decoder", "GQCO")
	if err != nil {
		return out, err
	}
	out.Collapse = reader.U32()
	out.Range, out.Val = reader.U32(), reader.U32()
	out.Tell, out.TellFrac, out.Error = reader.U32(), reader.U32(), reader.U32()
	n := reader.Count(p.N)
	out.Samples = make([]int32, n)
	for i := range out.Samples {
		out.Samples[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return CELTFixedQEXTCubicResult{}, err
	}
	return out, nil
}
