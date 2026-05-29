package libopustest

import "fmt"

const (
	celtAntiCollapseInputMagic  = "GACI"
	celtAntiCollapseOutputMagic = "GACO"

	// CELTAntiCollapseModeRenormalise selects the FIXED_POINT
	// renormalise_vector kernel.
	CELTAntiCollapseModeRenormalise = uint32(0)
	// CELTAntiCollapseModeAntiCollapse selects the FIXED_POINT anti_collapse
	// kernel.
	CELTAntiCollapseModeAntiCollapse = uint32(1)
)

var celtAntiCollapseHelper HelperCache

func buildCELTAntiCollapseHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "CELT fixed anti-collapse",
		OutputBase:  "gopus_libopus_celt_anticollapse_fixed",
		SourceFile:  "libopus_celt_anticollapse_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// ProbeCELTRenormaliseVector runs the FIXED_POINT renormalise_vector kernel
// against the real libopus reference. x is the celt_norm (int32) input vector;
// gain is the Q31 gain. It returns the renormalised vector of the same length.
func ProbeCELTRenormaliseVector(x []int32, gain int32) ([]int32, error) {
	binPath, buildErr := celtAntiCollapseHelper.Path(buildCELTAntiCollapseHelper)
	if buildErr != nil {
		return nil, buildErr
	}
	n := len(x)
	payload := NewOraclePayload(celtAntiCollapseInputMagic, CELTAntiCollapseModeRenormalise)
	payload.U32(uint32(n))
	payload.I32(gain)
	payload.I32s(x...)

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed anti-collapse", celtAntiCollapseOutputMagic)
	if runErr != nil {
		return nil, runErr
	}
	if mode := reader.U32(); mode != CELTAntiCollapseModeRenormalise {
		return nil, fmt.Errorf("celt anti-collapse oracle: unexpected mode %d want %d", mode, CELTAntiCollapseModeRenormalise)
	}
	out := make([]int32, n)
	for i := range out {
		out[i] = reader.I32()
	}
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return nil, cErr
	}
	return out, nil
}

// CELTAntiCollapseInput bundles the anti_collapse kernel arguments. The arrays
// follow the libopus layout: X is C*Size celt_norm samples, collapseMasks is
// NbEBands*C bytes, logE/prev1logE/prev2logE are 2*NbEBands celt_glog (Q24)
// each (libopus always allocates both channels; the !encode && C==1 path reads
// the 2nd-channel slot), pulses is NbEBands ints, and eBands is NbEBands+1
// entries.
type CELTAntiCollapseInput struct {
	X             []int32
	CollapseMasks []byte
	LM            int
	C             int
	Size          int
	Start         int
	End           int
	LogE          []int32
	Prev1LogE     []int32
	Prev2LogE     []int32
	Pulses        []int
	EBands        []int16
	NbEBands      int
	Seed          uint32
	Encode        bool
}

// ProbeCELTAntiCollapse runs the FIXED_POINT anti_collapse kernel against the
// real libopus reference and returns the modified X vector.
func ProbeCELTAntiCollapse(in CELTAntiCollapseInput) ([]int32, error) {
	binPath, buildErr := celtAntiCollapseHelper.Path(buildCELTAntiCollapseHelper)
	if buildErr != nil {
		return nil, buildErr
	}
	payload := NewOraclePayload(celtAntiCollapseInputMagic, CELTAntiCollapseModeAntiCollapse)
	payload.U32(uint32(in.LM))
	payload.U32(uint32(in.C))
	payload.U32(uint32(in.Size))
	payload.U32(uint32(in.Start))
	payload.U32(uint32(in.End))
	payload.U32(uint32(in.NbEBands))
	payload.U32(in.Seed)
	if in.Encode {
		payload.U32(1)
	} else {
		payload.U32(0)
	}
	for _, v := range in.EBands {
		payload.I32(int32(v))
	}
	payload.I32s(in.X...)
	for _, v := range in.CollapseMasks {
		payload.I32(int32(v))
	}
	payload.I32s(in.LogE...)
	payload.I32s(in.Prev1LogE...)
	payload.I32s(in.Prev2LogE...)
	for _, v := range in.Pulses {
		payload.I32(int32(v))
	}

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed anti-collapse", celtAntiCollapseOutputMagic)
	if runErr != nil {
		return nil, runErr
	}
	if mode := reader.U32(); mode != CELTAntiCollapseModeAntiCollapse {
		return nil, fmt.Errorf("celt anti-collapse oracle: unexpected mode %d want %d", mode, CELTAntiCollapseModeAntiCollapse)
	}
	out := make([]int32, len(in.X))
	for i := range out {
		out[i] = reader.I32()
	}
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return nil, cErr
	}
	return out, nil
}
