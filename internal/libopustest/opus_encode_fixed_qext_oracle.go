package libopustest

var opusEncodeFixedQEXTHelper HelperCache

func buildOpusEncodeFixedQEXTHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "opus encode fixed-QEXT",
		OutputBase:   "gopus_libopus_opus_encode_fixed_qext",
		SourceFile:   "libopus_opus_encode_fixed_qext_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk", "src"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeOpusEncodeFixedQEXTMixedRecords runs the public mixed-input probe against
// the selected FIXED_POINT + ENABLE_QEXT libopus archive. Its C helper enables
// QEXT through OPUS_SET_QEXT(1) before any input frame is encoded.
func ProbeOpusEncodeFixedQEXTMixedRecords(p OpusEncodeFixedParams, frames []OpusEncodeFixedMixedFrame) ([]OpusEncodeFixedRecord, error) {
	binPath, err := opusEncodeFixedQEXTHelper.Path(buildOpusEncodeFixedQEXTHelper)
	if err != nil {
		return nil, err
	}
	return probeOpusEncodeMixedRecords(binPath, p, frames)
}
