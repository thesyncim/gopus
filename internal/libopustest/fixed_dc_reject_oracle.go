package libopustest

import "fmt"

var fixedDCRejectHelper HelperCache

func buildFixedDCRejectHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "fixed dc reject",
		OutputBase:  "gopus_libopus_fixed_dc_reject",
		SourceFile:  "libopus_fixed_dc_reject_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// ProbeFixedDCReject calls the selected FIXED_POINT+ENABLE_RES24 libopus
// dc_reject with Q8 opus_res samples and returns all defined output and state.
func ProbeFixedDCReject(input []int32, mem [4]int32, fs, channels, cutoff int) ([]int32, [4]int32, error) {
	validRate := fs == 8000 || fs == 12000 || fs == 16000 || fs == 24000 || fs == 48000
	if channels < 1 || channels > 2 || !validRate || cutoff != 3 || len(input) == 0 || len(input)%channels != 0 || len(input)/channels > 5760 {
		return nil, mem, fmt.Errorf("fixed dc reject: invalid dimensions")
	}
	binPath, err := fixedDCRejectHelper.Path(buildFixedDCRejectHelper)
	if err != nil {
		return nil, mem, err
	}
	payload := NewOraclePayload("GOFD")
	for _, v := range []uint32{uint32(fs), uint32(channels), uint32(len(input) / channels), uint32(cutoff)} {
		payload.U32(v)
	}
	for _, v := range mem {
		payload.I32(v)
	}
	for _, v := range input {
		payload.I32(v)
	}
	reader, err := RunOracle(binPath, payload.Bytes(), "fixed dc reject", "GOFO")
	if err != nil {
		return nil, mem, err
	}
	if got := reader.Count(len(input)); got != len(input) {
		return nil, mem, fmt.Errorf("fixed dc reject: output count=%d want=%d", got, len(input))
	}
	for i := range mem {
		mem[i] = int32(reader.U32())
	}
	out := make([]int32, len(input))
	for i := range out {
		out[i] = int32(reader.U32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, mem, err
	}
	return out, mem, nil
}
