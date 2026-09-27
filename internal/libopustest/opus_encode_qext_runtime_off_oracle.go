package libopustest

var opusEncodeQEXTRuntimeOffHelper HelperCache

func buildOpusEncodeQEXTRuntimeOffHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "opus encode QEXT runtime-off",
		OutputBase:  "gopus_libopus_opus_encode_qext_runtime_off",
		SourceFile:  "libopus_opus_encode_fixed_info.c",
		QEXTRef:     true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{QEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// ProbeOpusEncodeFloatQEXTRuntimeOffMixedRecords compares the ordinary float
// build against ENABLE_QEXT with OPUS_SET_QEXT left disabled.
func ProbeOpusEncodeFloatQEXTRuntimeOffMixedRecords(p OpusEncodeFixedParams, frames []OpusEncodeFixedMixedFrame) ([]OpusEncodeFixedRecord, error) {
	binPath, err := opusEncodeQEXTRuntimeOffHelper.Path(buildOpusEncodeQEXTRuntimeOffHelper)
	if err != nil {
		return nil, err
	}
	return probeOpusEncodeMixedRecords(binPath, p, frames)
}
