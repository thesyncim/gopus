package libopustest

var opusEncodeFixedQEXTRuntimeOffHelper HelperCache

func buildOpusEncodeFixedQEXTRuntimeOffHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "opus encode fixed-QEXT runtime-off",
		OutputBase:   "gopus_libopus_opus_encode_fixed_qext_runtime_off",
		SourceFile:   "libopus_opus_encode_fixed_info.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk", "src"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords runs the fixed public encoder
// against the paired ENABLE_QEXT archive without setting OPUS_SET_QEXT. This
// covers the combined build's default runtime-off path with matching Q31 CELT
// tables and transforms.
func ProbeOpusEncodeFixedQEXTRuntimeOffMixedRecords(p OpusEncodeFixedParams, frames []OpusEncodeFixedMixedFrame) ([]OpusEncodeFixedRecord, error) {
	binPath, err := opusEncodeFixedQEXTRuntimeOffHelper.Path(buildOpusEncodeFixedQEXTRuntimeOffHelper)
	if err != nil {
		return nil, err
	}
	return probeOpusEncodeMixedRecords(binPath, p, frames)
}
