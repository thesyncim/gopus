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

// ProbeFixedHPCutoff runs sequential FIXED_POINT+ENABLE_RES24 hp_cutoff frames
// from src/opus_encoder.c. Each frame has a cutoff selected by the caller; the
// four opus_val32 state words carry between frames and are returned per frame.
func ProbeFixedHPCutoff(inputFrames [][]int32, mem [4]int32, fs, channels int, cutoffs []int) ([][]int32, [][4]int32, error) {
	validRate := fs == 8000 || fs == 12000 || fs == 16000 || fs == 24000 || fs == 48000
	if channels < 1 || channels > 2 || !validRate || len(inputFrames) == 0 || len(inputFrames) > 32 ||
		len(inputFrames) != len(cutoffs) || len(inputFrames[0]) == 0 || len(inputFrames[0])%channels != 0 || len(inputFrames[0])/channels > 5760 {
		return nil, nil, fmt.Errorf("fixed hp cutoff: invalid dimensions")
	}
	frameSamples := len(inputFrames[0])
	frameSize := frameSamples / channels
	for frame, input := range inputFrames {
		if len(input) != frameSamples || cutoffs[frame] < 60 || cutoffs[frame] > 100 {
			return nil, nil, fmt.Errorf("fixed hp cutoff: invalid frame %d", frame)
		}
	}
	binPath, err := fixedDCRejectHelper.Path(buildFixedDCRejectHelper)
	if err != nil {
		return nil, nil, err
	}
	payload := NewOraclePayloadVersion("GOFD", 2,
		uint32(fs), uint32(channels), uint32(frameSize), uint32(len(inputFrames)))
	payload.I32s(mem[:]...)
	for frame, input := range inputFrames {
		payload.U32(uint32(cutoffs[frame]))
		payload.I32s(input...)
	}
	reader, err := RunOracleVersion(binPath, payload.Bytes(), "fixed hp cutoff", "GOFO", 2)
	if err != nil {
		return nil, nil, err
	}
	if got := reader.Count(len(inputFrames)); got != len(inputFrames) {
		return nil, nil, fmt.Errorf("fixed hp cutoff: frames=%d want=%d", got, len(inputFrames))
	}
	outputs := make([][]int32, len(inputFrames))
	states := make([][4]int32, len(inputFrames))
	for frame := range inputFrames {
		if got := reader.Count(frameSamples); got != frameSamples {
			return nil, nil, fmt.Errorf("fixed hp cutoff: frame %d count=%d want=%d", frame, got, frameSamples)
		}
		for i := range states[frame] {
			states[frame][i] = reader.I32()
		}
		outputs[frame] = make([]int32, frameSamples)
		for i := range outputs[frame] {
			outputs[frame][i] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, nil, err
	}
	return outputs, states, nil
}
