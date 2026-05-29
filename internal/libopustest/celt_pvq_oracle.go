package libopustest

import "fmt"

const (
	celtPVQInputMagic  = "GPVI"
	celtPVQOutputMagic = "GPVO"

	// CELTPVQModeSearch selects the FIXED_POINT op_pvq_search_c kernel.
	CELTPVQModeSearch = uint32(0)
)

var celtPVQHelper HelperCache

func buildCELTPVQHelper() (string, error) {
	// Build against the --enable-fixed-point reference so the integer
	// op_pvq_search_c kernel is exercised. The libopus static library is linked
	// to resolve op_pvq_search_c itself (defined in vq.c) and celt_fatal.
	return BuildCHelper(CHelperConfig{
		Label:       "CELT fixed pvq",
		OutputBase:  "gopus_libopus_celt_pvq_fixed",
		SourceFile:  "libopus_celt_pvq_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// ProbeCELTPVQSearch runs the FIXED_POINT op_pvq_search_c kernel against the
// real libopus reference. X is the celt_norm (int32) input vector of length N;
// the kernel modifies its own copy internally. It returns yy (the squared norm
// of the codeword, sign-extended from opus_val16) and iy, the per-bin signed
// pulse counts of length N.
func ProbeCELTPVQSearch(x []int32, k int) (yy int32, iy []int32, err error) {
	binPath, buildErr := celtPVQHelper.Path(buildCELTPVQHelper)
	if buildErr != nil {
		return 0, nil, buildErr
	}
	n := len(x)
	payload := NewOraclePayload(celtPVQInputMagic, CELTPVQModeSearch)
	payload.U32(uint32(n))
	payload.U32(uint32(k))
	payload.I32s(x...)

	reader, runErr := RunOracle(binPath, payload.Bytes(), "CELT fixed pvq", celtPVQOutputMagic)
	if runErr != nil {
		return 0, nil, runErr
	}
	if mode := reader.U32(); mode != CELTPVQModeSearch {
		return 0, nil, fmt.Errorf("celt pvq oracle: unexpected mode %d want %d", mode, CELTPVQModeSearch)
	}
	yy = reader.I32()
	iy = make([]int32, n)
	for i := range iy {
		iy[i] = reader.I32()
	}
	if cErr := reader.ExpectConsumed(); cErr != nil {
		return 0, nil, cErr
	}
	return yy, iy, nil
}
