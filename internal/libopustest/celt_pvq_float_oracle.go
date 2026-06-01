package libopustest

import (
	"fmt"
	"math"
)

const (
	celtPVQFloatInputMagic  = "GPFI"
	celtPVQFloatOutputMagic = "GPFO"

	celtPVQFloatModeSearch = uint32(0)
)

var celtPVQFloatHelper HelperCache

func buildCELTPVQFloatHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "CELT float pvq",
		OutputBase:  "gopus_libopus_celt_pvq_float",
		SourceFile:  "libopus_celt_pvq_float_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// ProbeCELTPVQSearchFloat runs the FLOAT op_pvq_search_c kernel from the
// default (float) libopus reference. x is the celt_norm (float32) input vector;
// it returns yy (codeword squared norm, float32) and iy (signed pulse counts).
// This is the same-arch float oracle for the encoder PVQ search.
func ProbeCELTPVQSearchFloat(x []float32, k int) (yy float32, iy []int32, err error) {
	binPath, buildErr := celtPVQFloatHelper.Path(buildCELTPVQFloatHelper)
	if buildErr != nil {
		return 0, nil, buildErr
	}
	n := len(x)
	payload := NewOraclePayload(celtPVQFloatInputMagic, celtPVQFloatModeSearch)
	payload.U32(uint32(n))
	payload.U32(uint32(k))
	for _, v := range x {
		payload.U32(math.Float32bits(v))
	}

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT float pvq", celtPVQFloatOutputMagic)
	if runErr != nil {
		return 0, nil, runErr
	}
	if mode := reader.U32(); mode != celtPVQFloatModeSearch {
		return 0, nil, fmt.Errorf("celt float pvq oracle: unexpected mode %d", mode)
	}
	yy = math.Float32frombits(uint32(reader.I32()))
	iy = make([]int32, n)
	for i := range iy {
		iy[i] = reader.I32()
	}
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return 0, nil, cErr
	}
	return yy, iy, nil
}
