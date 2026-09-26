package libopustest

import "fmt"

const (
	gainFadeInputMagic  = "GGFI"
	gainFadeOutputMagic = "GGFO"
)

var gainFadeHelper HelperCache

// GainFadeParams describes one in-place call to libopus's gain_fade().
type GainFadeParams struct {
	SampleRate int
	Channels   int
	G1         float32
	G2         float32
	Samples    []float32
}

func buildGainFadeHelper() (string, error) {
	return BuildCHelper(CHelperConfig{
		Label:       "libopus gain fade",
		OutputBase:  "gopus_libopus_gain_fade",
		SourceFile:  "libopus_gain_fade_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

// ProbeGainFade runs the actual pinned static gain_fade() implementation
// in-place against every supplied input frame.
func ProbeGainFade(cases []GainFadeParams) ([][]float32, error) {
	if len(cases) == 0 {
		return nil, nil
	}
	payload := NewOraclePayload(gainFadeInputMagic)
	payload.U32(uint32(len(cases)))
	for i, tc := range cases {
		if tc.SampleRate != 24000 && tc.SampleRate != 48000 {
			return nil, fmt.Errorf("gain fade case %d has unsupported sample rate %d", i, tc.SampleRate)
		}
		if tc.Channels != 1 && tc.Channels != 2 {
			return nil, fmt.Errorf("gain fade case %d has invalid channel count %d", i, tc.Channels)
		}
		if len(tc.Samples) == 0 || len(tc.Samples)%tc.Channels != 0 {
			return nil, fmt.Errorf("gain fade case %d has invalid sample count %d for %d channels", i, len(tc.Samples), tc.Channels)
		}
		frameSize := len(tc.Samples) / tc.Channels
		minFrameSize := tc.SampleRate / 400
		if frameSize < minFrameSize {
			return nil, fmt.Errorf("gain fade case %d frame size %d below minimum %d", i, frameSize, minFrameSize)
		}
		payload.U32(uint32(tc.SampleRate))
		payload.U32(uint32(tc.Channels))
		payload.U32(uint32(frameSize))
		payload.Float32(tc.G1)
		payload.Float32(tc.G2)
		payload.U32(uint32(len(tc.Samples)))
		payload.Float32s(tc.Samples...)
	}

	binPath, err := gainFadeHelper.Path(buildGainFadeHelper)
	if err != nil {
		return nil, err
	}
	reader, err := RunOracle(binPath, payload.Bytes(), "libopus gain fade", gainFadeOutputMagic)
	if err != nil {
		return nil, err
	}
	if got := reader.Count(len(cases)); reader.Err() != nil {
		return nil, reader.Err()
	} else if got != len(cases) {
		return nil, fmt.Errorf("libopus gain fade helper count=%d want %d", got, len(cases))
	}

	outputs := make([][]float32, len(cases))
	for i, tc := range cases {
		count := int(reader.U32())
		if reader.Err() != nil {
			return nil, reader.Err()
		}
		if count != len(tc.Samples) {
			return nil, fmt.Errorf("libopus gain fade helper case %d output samples=%d want %d", i, count, len(tc.Samples))
		}
		outputs[i] = make([]float32, count)
		for j := range outputs[i] {
			outputs[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return outputs, nil
}
