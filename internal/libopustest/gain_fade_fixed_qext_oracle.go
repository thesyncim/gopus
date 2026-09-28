//go:build gopus_fixed_point && gopus_qext

package libopustest

import "fmt"

type FixedQEXTGainFadeCase struct {
	SampleRate int
	Channels   int
	FrameSize  int
	G1, G2     int16
	Samples    []int32
}

var fixedQEXTGainFadeHelper HelperCache

func buildFixedQEXTGainFadeHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:        "fixed-QEXT opus_encoder.c gain fade",
		OutputBase:   "gopus_libopus_gain_fade_fixed_qext",
		SourceFile:   "libopus_gain_fade_fixed_qext_info.c",
		ProbeRelPath: "src/opus_encoder.c",
		FixedQEXTRef: true,
		CFlags:       []string{"-DHAVE_CONFIG_H", "-DENABLE_QEXT", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk", "src"},
		Libs:         []string{FixedQEXTRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
}

// ProbeFixedQEXTGainFade evaluates the pinned opus_encoder.c gain_fade for a
// batch of fixed-QEXT frames using the selected scalar or SIMD reference build.
func ProbeFixedQEXTGainFade(cases []FixedQEXTGainFadeCase) ([][]int32, error) {
	if len(cases) == 0 || len(cases) > 64 {
		return nil, fmt.Errorf("fixed-QEXT gain fade: case count %d outside [1,64]", len(cases))
	}
	payload := NewOraclePayload("GGQI", uint32(len(cases)))
	for i, c := range cases {
		switch c.SampleRate {
		case 8000, 12000, 16000, 24000, 48000, 96000:
		default:
			return nil, fmt.Errorf("fixed-QEXT gain fade: case %d unsupported sample rate %d", i, c.SampleRate)
		}
		if c.Channels < 1 || c.Channels > 2 || c.FrameSize < c.SampleRate/400 ||
			c.FrameSize > 3840 || c.G1 < 0 || c.G1 > 32767 || c.G2 < 0 || c.G2 > 32767 {
			return nil, fmt.Errorf("fixed-QEXT gain fade: case %d has invalid parameters", i)
		}
		wantSamples := c.FrameSize * c.Channels
		if len(c.Samples) != wantSamples {
			return nil, fmt.Errorf("fixed-QEXT gain fade: case %d has %d samples, want %d", i, len(c.Samples), wantSamples)
		}
		payload.U32(uint32(c.SampleRate))
		payload.U32(uint32(c.Channels))
		payload.U32(uint32(c.FrameSize))
		payload.U32(uint32(c.G1))
		payload.U32(uint32(c.G2))
		payload.U32(uint32(wantSamples))
		payload.I32s(c.Samples...)
	}

	bin, err := fixedQEXTGainFadeHelper.Path(buildFixedQEXTGainFadeHelper)
	if err != nil {
		return nil, err
	}
	reader, err := RunOracle(bin, payload.Bytes(), "fixed-QEXT opus_encoder.c gain fade", "GGQO")
	if err != nil {
		return nil, err
	}
	reader.Count(len(cases))
	out := make([][]int32, len(cases))
	for i, c := range cases {
		count := c.FrameSize * c.Channels
		reader.Count(count)
		out[i] = make([]int32, count)
		for j := range out[i] {
			out[i][j] = reader.I32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}
