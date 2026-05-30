package libopustest

import (
	"fmt"
	"math"
)

const (
	celtAllocAnalysisInputMagic  = "GAAI"
	celtAllocAnalysisOutputMagic = "GAAO"

	// CELTAllocAnalysisModeTFAnalysis selects the FIXED_POINT tf_analysis kernel.
	CELTAllocAnalysisModeTFAnalysis = uint32(0)
	// CELTAllocAnalysisModeTFEncode selects the FIXED_POINT tf_encode kernel.
	CELTAllocAnalysisModeTFEncode = uint32(1)
	// CELTAllocAnalysisModeAllocTrim selects the FIXED_POINT alloc_trim_analysis kernel.
	CELTAllocAnalysisModeAllocTrim = uint32(2)
)

var celtAllocAnalysisHelper HelperCache

func buildCELTAllocAnalysisHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "CELT fixed alloc analysis",
		OutputBase:  "gopus_libopus_celt_alloc_analysis_fixed",
		SourceFile:  "libopus_celt_alloc_analysis_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// ProbeCELTTFAnalysis runs the FIXED_POINT tf_analysis kernel. eBands holds
// nbEBands+1 band boundaries; X holds the normalised MDCT bins (celt_norm,
// channel-interleaved at N0). tfEstimate is Q14. It returns tf_select and the
// per-band tf_res decisions (length len).
func ProbeCELTTFAnalysis(eBands []int16, length int, isTransient bool, lambda int, x []int32, n0, lm int, tfEstimate int16, tfChan int, importance []int) (tfSelect int, tfRes []int, err error) {
	binPath, buildErr := celtAllocAnalysisHelper.Path(buildCELTAllocAnalysisHelper)
	if buildErr != nil {
		return 0, nil, buildErr
	}
	nbEBands := len(eBands) - 1
	payload := NewOraclePayload(celtAllocAnalysisInputMagic, CELTAllocAnalysisModeTFAnalysis)
	payload.U32(uint32(nbEBands))
	payload.U32(uint32(length))
	payload.U32(boolU32(isTransient))
	payload.I32(int32(lambda))
	payload.U32(uint32(n0))
	payload.U32(uint32(lm))
	payload.I32(int32(tfEstimate))
	payload.U32(uint32(tfChan))
	for _, b := range eBands {
		payload.I32(int32(b))
	}
	payload.U32(uint32(len(x)))
	payload.I32s(x...)
	for _, v := range importance {
		payload.I32(int32(v))
	}

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed alloc analysis", celtAllocAnalysisOutputMagic)
	if runErr != nil {
		return 0, nil, runErr
	}
	if mode := reader.U32(); mode != CELTAllocAnalysisModeTFAnalysis {
		return 0, nil, fmt.Errorf("celt alloc analysis oracle: unexpected mode %d want %d", mode, CELTAllocAnalysisModeTFAnalysis)
	}
	tfSelect = int(reader.I32())
	n := int(reader.U32())
	tfRes = make([]int, n)
	for i := range tfRes {
		tfRes[i] = int(reader.I32())
	}
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return 0, nil, cErr
	}
	return tfSelect, tfRes, nil
}

// ProbeCELTTFEncode runs the FIXED_POINT tf_encode kernel. It encodes tfRes (and
// the tf_select bit) into a range coder of bufSize bytes, after consuming preBits
// of priming bits (each an ec_enc_bit_logp(0,1) call) to set the bit budget. It
// returns the finished coder buffer and the rewritten tf_res array (length end).
func ProbeCELTTFEncode(start, end int, isTransient bool, tfRes []int, lm, tfSelect, bufSize, preBits int) (buf []byte, outTFRes []int, err error) {
	binPath, buildErr := celtAllocAnalysisHelper.Path(buildCELTAllocAnalysisHelper)
	if buildErr != nil {
		return nil, nil, buildErr
	}
	payload := NewOraclePayload(celtAllocAnalysisInputMagic, CELTAllocAnalysisModeTFEncode)
	payload.U32(uint32(start))
	payload.U32(uint32(end))
	payload.U32(boolU32(isTransient))
	payload.U32(uint32(lm))
	payload.U32(uint32(tfSelect))
	payload.U32(uint32(bufSize))
	payload.U32(uint32(preBits))
	for _, v := range tfRes {
		payload.I32(int32(v))
	}

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed alloc analysis", celtAllocAnalysisOutputMagic)
	if runErr != nil {
		return nil, nil, runErr
	}
	if mode := reader.U32(); mode != CELTAllocAnalysisModeTFEncode {
		return nil, nil, fmt.Errorf("celt alloc analysis oracle: unexpected mode %d want %d", mode, CELTAllocAnalysisModeTFEncode)
	}
	bn := int(reader.U32())
	buf = append([]byte(nil), reader.Bytes(bn)...)
	en := int(reader.U32())
	outTFRes = make([]int, en)
	for i := range outTFRes {
		outTFRes[i] = int(reader.I32())
	}
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return nil, nil, cErr
	}
	return buf, outTFRes, nil
}

// CELTAllocTrimResult mirrors the outputs of alloc_trim_analysis.
type CELTAllocTrimResult struct {
	TrimIndex    int
	StereoSaving int16
}

// ProbeCELTAllocTrimAnalysis runs the FIXED_POINT alloc_trim_analysis kernel.
// eBands holds nbEBands+1 boundaries, X holds channel-interleaved celt_norm bins
// at N0, bandLogE holds 2*nbEBands celt_glog (Q24) energies. stereoSaving is the
// running Q8 estimate, tfEstimate is Q14, surroundTrim is Q24.
func ProbeCELTAllocTrimAnalysis(eBands []int16, x []int32, bandLogE []int32, end, lm, c, n0 int, stereoSaving, tfEstimate int16, intensity int, surroundTrim, equivRate int32, analysisValid bool, analysisTonalitySlope float32) (CELTAllocTrimResult, error) {
	binPath, buildErr := celtAllocAnalysisHelper.Path(buildCELTAllocAnalysisHelper)
	if buildErr != nil {
		return CELTAllocTrimResult{}, buildErr
	}
	nbEBands := len(eBands) - 1
	payload := NewOraclePayload(celtAllocAnalysisInputMagic, CELTAllocAnalysisModeAllocTrim)
	payload.U32(uint32(nbEBands))
	payload.U32(uint32(end))
	payload.U32(uint32(lm))
	payload.U32(uint32(c))
	payload.U32(uint32(n0))
	payload.I32(int32(stereoSaving))
	payload.I32(int32(tfEstimate))
	payload.U32(uint32(intensity))
	payload.I32(surroundTrim)
	payload.I32(equivRate)
	payload.U32(boolU32(analysisValid))
	payload.U32(math.Float32bits(analysisTonalitySlope))
	for _, b := range eBands {
		payload.I32(int32(b))
	}
	payload.U32(uint32(len(x)))
	payload.I32s(x...)
	payload.U32(uint32(len(bandLogE)))
	payload.I32s(bandLogE...)

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed alloc analysis", celtAllocAnalysisOutputMagic)
	if runErr != nil {
		return CELTAllocTrimResult{}, runErr
	}
	if mode := reader.U32(); mode != CELTAllocAnalysisModeAllocTrim {
		return CELTAllocTrimResult{}, fmt.Errorf("celt alloc analysis oracle: unexpected mode %d want %d", mode, CELTAllocAnalysisModeAllocTrim)
	}
	res := CELTAllocTrimResult{
		TrimIndex:    int(reader.I32()),
		StereoSaving: int16(reader.I32()),
	}
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return CELTAllocTrimResult{}, cErr
	}
	return res, nil
}
