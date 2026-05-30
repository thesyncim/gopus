package libopustest

const (
	celtPLCInputMagic  = "GPLI"
	celtPLCOutputMagic = "GPLO"
)

var celtPLCHelper HelperCache

func buildCELTPLCHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt plc fixed",
		OutputBase:  "gopus_libopus_celt_plc_fixed",
		SourceFile:  "libopus_celt_plc_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTPLCHelperPath() (string, error) {
	return celtPLCHelper.Path(buildCELTPLCHelper)
}

// ProbeCELTFixedPLC primes a FIXED_POINT libopus CELTDecoder with one prior-good
// CELT packet (static 48000/960 mode), then runs numLost consecutive lost frames
// through the celt_decode_with_ec PLC path (data==NULL), returning the concealed
// int16 PCM frame-major: numLost blocks of channels*frameSize samples.
func ProbeCELTFixedPLC(packet []byte, channels, frameSize, start, end, numLost int) ([]int16, error) {
	binPath, err := getCELTPLCHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtPLCInputMagic, 0, 0)
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(start))
	payload.U32(uint32(end))
	payload.U32(uint32(numLost))
	payload.U32(uint32(len(packet)))
	payload.Raw(packet)
	if pad := (4 - len(packet)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed plc", celtPLCOutputMagic)
	if err != nil {
		return nil, err
	}
	want := numLost * channels * frameSize
	count := reader.Count(want)
	reader.ExpectRemaining(2 * count)
	out := make([]int16, count)
	for i := range out {
		out[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
