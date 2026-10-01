//go:build gopus_fixed_point && gopus_qext

package libopustest

import "fmt"

type FixedQEXTSmoothFadeParams struct {
	SampleRate, Channels, Overlap int
	In1, In2                      []int32
}

var fixedQEXTSmoothFadeHelper HelperCache

func buildFixedQEXTSmoothFadeHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT opus_res smooth fade",
		OutputBase:   "gopus_libopus_smooth_fade_fixed_qext",
		SourceFile:   "libopus_smooth_fade_fixed_qext_info.c",
		ProbeRelPath: "src/opus_decoder.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeFixedQEXTSmoothFade evaluates the fixed-QEXT smooth_fade arithmetic
// against the selected FIXED_POINT+ENABLE_QEXT CELT window tables.
func ProbeFixedQEXTSmoothFade(p FixedQEXTSmoothFadeParams) ([]int32, error) {
	switch p.SampleRate {
	case 8000, 12000, 16000, 24000, 48000, 96000:
	default:
		return nil, fmt.Errorf("fixed-QEXT smooth fade: unsupported sample rate %d", p.SampleRate)
	}
	if p.Channels < 1 || p.Channels > 2 || p.Overlap != p.SampleRate/400 {
		return nil, fmt.Errorf("fixed-QEXT smooth fade: invalid dimensions")
	}
	count := p.Overlap * p.Channels
	if len(p.In1) != count || len(p.In2) != count {
		return nil, fmt.Errorf("fixed-QEXT smooth fade: inputs have lengths %d/%d, want %d", len(p.In1), len(p.In2), count)
	}
	bin, err := fixedQEXTSmoothFadeHelper.Path(buildFixedQEXTSmoothFadeHelper)
	if err != nil {
		return nil, err
	}
	payload := NewOraclePayloadVersion("GSQI", 1,
		uint32(p.SampleRate), uint32(p.Channels), uint32(p.Overlap), uint32(count))
	payload.I32s(p.In1...)
	payload.I32s(p.In2...)
	reader, err := RunOracle(bin, payload.Bytes(), "fixed-QEXT opus_res smooth fade", "GSQO")
	if err != nil {
		return nil, err
	}
	reader.Count(count)
	out := make([]int32, count)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
