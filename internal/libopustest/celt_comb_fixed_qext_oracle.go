package libopustest

import (
	"fmt"
)

// CELTFixedQEXTCombParams describes a separated-buffer CELT comb filter call
// with the Q31 coefficient window used by ENABLE_QEXT.
type CELTFixedQEXTCombParams struct {
	Y, X               []int32
	YOffset, XOffset   int
	N, T0, T1, Overlap int
	G0, G1             int16
	Tapset0, Tapset1   int
	Window             []int32
}

var celtCombFixedQEXTHelper HelperCache

func buildCELTCombFixedQEXTHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "CELT fixed-QEXT Q31 comb filter",
		OutputBase:   "gopus_libopus_celt_comb_fixed_qext",
		SourceFile:   "libopus_celt_comb_fixed_qext_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeCELTFixedQEXTComb evaluates one distinct-source/destination comb filter
// call against the selected FIXED_POINT + ENABLE_QEXT libopus archive.
func ProbeCELTFixedQEXTComb(p CELTFixedQEXTCombParams) ([]int32, error) {
	if p.N <= 0 || p.N > 100000 || p.YOffset < 0 || p.XOffset < 0 ||
		p.YOffset+p.N > len(p.Y) || p.XOffset+p.N > len(p.X) ||
		p.Overlap < 0 || p.Overlap > p.N || p.Overlap > 240 || len(p.Window) != p.Overlap ||
		p.T0 < 0 || p.T1 < 0 || p.Tapset0 < 0 || p.Tapset0 > 2 || p.Tapset1 < 0 || p.Tapset1 > 2 ||
		p.XOffset < max(p.T0, p.T1, 15)+2 {
		return nil, fmt.Errorf("invalid Q31 CELT comb filter dimensions")
	}
	bin, err := celtCombFixedQEXTHelper.Path(buildCELTCombFixedQEXTHelper)
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayloadVersion("GQCI", 1,
		uint32(len(p.Y)), uint32(p.YOffset), uint32(len(p.X)), uint32(p.XOffset),
		uint32(p.N), uint32(p.Overlap), uint32(p.T0), uint32(p.T1),
		uint32(int32(p.G0)), uint32(int32(p.G1)), uint32(p.Tapset0), uint32(p.Tapset1), uint32(len(p.Window)))
	payload.I32s(p.Y...)
	payload.I32s(p.X...)
	payload.I32s(p.Window...)
	reader, err := RunOracle(bin, payload.Bytes(), "CELT fixed-QEXT Q31 comb filter", "GQCO")
	if err != nil {
		return nil, err
	}
	reader.Count(p.N)
	out := make([]int32, p.N)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
