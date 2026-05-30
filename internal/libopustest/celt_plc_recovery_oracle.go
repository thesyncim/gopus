package libopustest

const (
	celtPLCRecoveryInputMagic  = "GPRI"
	celtPLCRecoveryOutputMagic = "GPRO"
)

var celtPLCRecoveryHelper HelperCache

func buildCELTPLCRecoveryHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "celt plc recovery fixed",
		OutputBase:  "gopus_libopus_celt_plc_recovery_fixed",
		SourceFile:  "libopus_celt_plc_recovery_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func getCELTPLCRecoveryHelperPath() (string, error) {
	return celtPLCRecoveryHelper.Path(buildCELTPLCRecoveryHelper)
}

// ProbeCELTFixedPLCRecovery primes a FIXED_POINT libopus CELTDecoder with one
// prior-good CELT packet (static 48000/960 mode), runs numLost consecutive lost
// frames through the celt_decode_with_ec PLC path (data==NULL), then decodes the
// good recovery packet (entered with loss_duration!=0) and returns the recovered
// good-frame int16 PCM (channels*frameSize samples).
func ProbeCELTFixedPLCRecovery(prime, good []byte, channels, frameSize, start, end, numLost int) ([]int16, error) {
	binPath, err := getCELTPLCRecoveryHelperPath()
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayload(celtPLCRecoveryInputMagic, 0, 0)
	payload.U32(uint32(channels))
	payload.U32(uint32(frameSize))
	payload.U32(uint32(start))
	payload.U32(uint32(end))
	payload.U32(uint32(numLost))
	payload.U32(uint32(len(prime)))
	payload.Raw(prime)
	if pad := (4 - len(prime)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}
	payload.U32(uint32(len(good)))
	payload.Raw(good)
	if pad := (4 - len(good)%4) % 4; pad > 0 {
		payload.Raw(make([]byte, pad))
	}

	reader, err := RunOracle(binPath, payload.Bytes(), "celt fixed plc recovery", celtPLCRecoveryOutputMagic)
	if err != nil {
		return nil, err
	}
	want := channels * frameSize
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
