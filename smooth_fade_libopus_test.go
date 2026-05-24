package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var smoothFadeHelper libopustest.HelperCache

func probeLibopusSmoothFade(sampleRate, channels, overlap int, in1, in2 []float32) ([]float32, error) {
	binPath, err := smoothFadeHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "smooth fade",
		OutputBase:   "gopus_libopus_smooth_fade",
		SourceFile:   "libopus_smooth_fade_info.c",
		ProbeRelPath: "src/opus_decoder.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "src"},
		Libs:         []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:    true,
	})
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(
		"GSFI",
		uint32(sampleRate),
		uint32(channels),
		uint32(overlap),
	)
	payload.Float32s(in1...)
	payload.Float32s(in2...)
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "smooth fade", "GSFO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(in1))
	reader.ExpectRemaining(count * 4)
	out := make([]float32, count)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSmoothFadeMatchesLibopusFloatPath(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name       string
		sampleRate int
		channels   int
		overlap    int
	}{
		{name: "48k_mono", sampleRate: 48000, channels: 1, overlap: 120},
		{name: "24k_stereo", sampleRate: 24000, channels: 2, overlap: 60},
		{name: "16k_stereo", sampleRate: 16000, channels: 2, overlap: 40},
		{name: "8k_mono", sampleRate: 8000, channels: 1, overlap: 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := tc.overlap * tc.channels
			in1 := make([]float32, count)
			in2 := make([]float32, count)
			for i := 0; i < count; i++ {
				x := float64(i + 1)
				in1[i] = float32(0.71*math.Sin(0.071*x) - 0.19*math.Cos(0.031*x))
				in2[i] = float32(-0.63*math.Cos(0.043*x) + 0.11*math.Sin(0.113*x))
			}

			want, err := probeLibopusSmoothFade(tc.sampleRate, tc.channels, tc.overlap, in1, in2)
			if err != nil {
				libopustest.HelperUnavailable(t, "smooth fade", err)
			}
			got := make([]float32, count)
			smoothFade(in1, in2, got, tc.overlap, tc.channels, tc.sampleRate)
			for i := range want {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("smoothFade[%d]=%08x want %08x (%0.10g vs %0.10g)",
						i, math.Float32bits(got[i]), math.Float32bits(want[i]), got[i], want[i])
				}
			}
		})
	}
}
