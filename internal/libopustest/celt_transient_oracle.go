package libopustest

import "fmt"

const (
	celtTransientInputMagic  = "GTRI"
	celtTransientOutputMagic = "GTRO"

	// CELTTransientModeAnalysis selects the FIXED_POINT transient_analysis
	// kernel.
	CELTTransientModeAnalysis = uint32(0)
	// CELTTransientModePatch selects the FIXED_POINT patch_transient_decision
	// kernel.
	CELTTransientModePatch = uint32(1)
)

var celtTransientHelper HelperCache

func buildCELTTransientHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "CELT fixed transient",
		OutputBase:  "gopus_libopus_celt_transient_fixed",
		SourceFile:  "libopus_celt_transient_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// CELTTransientResult mirrors the outputs of transient_analysis.
type CELTTransientResult struct {
	IsTransient   bool
	TFEstimate    int16
	TFChan        int
	WeakTransient bool
}

// ProbeCELTTransientAnalysis runs the FIXED_POINT transient_analysis kernel
// against the real libopus reference. in holds C*length celt_sig (int32)
// time-domain samples laid out as in[c*length+i]. toneFreq is Q13 and
// toneishness is Q29.
func ProbeCELTTransientAnalysis(in []int32, length, c int, allowWeakTransients bool, toneFreq int16, toneishness int32) (CELTTransientResult, error) {
	binPath, buildErr := celtTransientHelper.Path(buildCELTTransientHelper)
	if buildErr != nil {
		return CELTTransientResult{}, buildErr
	}
	payload := NewOraclePayload(celtTransientInputMagic, CELTTransientModeAnalysis)
	payload.U32(uint32(length))
	payload.U32(uint32(c))
	if allowWeakTransients {
		payload.U32(1)
	} else {
		payload.U32(0)
	}
	payload.I32(int32(toneFreq))
	payload.I32(toneishness)
	payload.I32s(in...)

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed transient", celtTransientOutputMagic)
	if runErr != nil {
		return CELTTransientResult{}, runErr
	}
	if mode := reader.U32(); mode != CELTTransientModeAnalysis {
		return CELTTransientResult{}, fmt.Errorf("celt transient oracle: unexpected mode %d want %d", mode, CELTTransientModeAnalysis)
	}
	res := CELTTransientResult{
		IsTransient:   reader.I32() != 0,
		TFEstimate:    int16(reader.I32()),
		TFChan:        int(reader.I32()),
		WeakTransient: reader.I32() != 0,
	}
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return CELTTransientResult{}, cErr
	}
	return res, nil
}

// ProbeCELTPatchTransientDecision runs the FIXED_POINT patch_transient_decision
// kernel. newE and oldE hold 2*nbEBands celt_glog (Q24) band energies laid out
// as E[c*nbEBands+i]; libopus always allocates both channels.
func ProbeCELTPatchTransientDecision(newE, oldE []int32, nbEBands, start, end, c int) (bool, error) {
	binPath, buildErr := celtTransientHelper.Path(buildCELTTransientHelper)
	if buildErr != nil {
		return false, buildErr
	}
	payload := NewOraclePayload(celtTransientInputMagic, CELTTransientModePatch)
	payload.U32(uint32(nbEBands))
	payload.U32(uint32(start))
	payload.U32(uint32(end))
	payload.U32(uint32(c))
	payload.I32s(newE...)
	payload.I32s(oldE...)

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed transient", celtTransientOutputMagic)
	if runErr != nil {
		return false, runErr
	}
	if mode := reader.U32(); mode != CELTTransientModePatch {
		return false, fmt.Errorf("celt transient oracle: unexpected mode %d want %d", mode, CELTTransientModePatch)
	}
	decision := reader.I32() != 0
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return false, cErr
	}
	return decision, nil
}
