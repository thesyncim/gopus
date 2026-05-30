package libopustest

import "fmt"

const (
	celtPrefilterInputMagic  = "GPRI"
	celtPrefilterOutputMagic = "GPRO"
)

var celtPrefilterHelper HelperCache

func buildCELTPrefilterHelper() (string, error) {
	// Build against the --enable-fixed-point reference so the value-producing
	// portion of run_prefilter is exercised through the real integer
	// pitch_downsample/pitch_search/remove_doubling kernels and the entropy
	// coder. libopus.a is linked to resolve those symbols.
	return BuildCHelper(CHelperConfig{
		Label:       "CELT fixed prefilter",
		OutputBase:  "gopus_libopus_celt_prefilter_fixed",
		SourceFile:  "libopus_celt_prefilter_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// CELTPrefilterParams mirrors the CELTEncoder state and per-frame inputs that
// the run_prefilter value-producing block consumes.
type CELTPrefilterParams struct {
	CC, N            int
	Complexity       int
	LossRate         int
	NbAvailableBytes int
	PrefilterPeriod  int
	PrefilterTapset  int
	Enabled          bool
	Hybrid           bool
	Tell             int
	TotalBits        int
	PrefilterGain    int16
	TFEstimate       int16
	ToneFreq         int16
	Toneishness      int32
	AnalysisValid    bool
	MaxPitchRatio    float32
}

// CELTPrefilterResult holds the FIXED_POINT run_prefilter decision values and
// the emitted post-filter parameter bytes.
type CELTPrefilterResult struct {
	PitchIndex int
	Gain       int16
	QG         int
	PFOn       bool
	Tapset     int
	Bytes      []byte
}

// ProbeCELTPrefilter runs the FIXED_POINT run_prefilter value-producing block
// against the real libopus reference. pre holds the assembled per-channel
// analysis buffers (each COMBFILTER_MAXPERIOD+N int32 samples); pre[1] is used
// only when p.CC==2.
func ProbeCELTPrefilter(pre [][]int32, p CELTPrefilterParams) (CELTPrefilterResult, error) {
	var res CELTPrefilterResult
	binPath, buildErr := celtPrefilterHelper.Path(buildCELTPrefilterHelper)
	if buildErr != nil {
		return res, buildErr
	}

	payload := NewOraclePayload(celtPrefilterInputMagic)
	payload.U32(uint32(p.CC))
	payload.U32(uint32(p.N))
	payload.I32(int32(p.Complexity))
	payload.I32(int32(p.LossRate))
	payload.I32(int32(p.NbAvailableBytes))
	payload.I32(int32(p.PrefilterPeriod))
	payload.I32(int32(p.PrefilterTapset))
	payload.I32(boolToI32(p.Enabled))
	payload.I32(boolToI32(p.Hybrid))
	payload.I32(int32(p.Tell))
	payload.I32(int32(p.TotalBits))
	payload.I16(p.PrefilterGain)
	payload.I16(p.TFEstimate)
	payload.I16(p.ToneFreq)
	payload.I32(p.Toneishness)
	payload.I32(boolToI32(p.AnalysisValid))
	payload.Float32(p.MaxPitchRatio)
	payload.I32s(pre[0]...)
	if p.CC == 2 {
		payload.I32s(pre[1]...)
	}

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed prefilter", celtPrefilterOutputMagic)
	if runErr != nil {
		return res, runErr
	}
	res.PitchIndex = int(reader.I32())
	res.Gain = int16(reader.I32())
	res.QG = int(reader.I32())
	res.PFOn = reader.I32() != 0
	res.Tapset = int(reader.I32())
	nbytes := int(reader.U32())
	res.Bytes = append([]byte(nil), reader.Bytes(nbytes)...)
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return res, fmt.Errorf("CELT fixed prefilter: %w", cErr)
	}
	return res, nil
}

func boolToI32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}
