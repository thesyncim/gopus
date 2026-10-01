//go:build gopus_fixed_point

package encoder

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// The selected fixed-point C estimator defines the Q15 width, its persistent
// state, and the integer mode threshold that consumes that width.
func TestFixedStereoWidthAndModeThresholdMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	bin, err := libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:        "selected fixed-point stereo width",
		OutputBase:   "gopus_libopus_fixed_stereo_width",
		SourceFile:   "libopus_fixed_stereo_width_info.c",
		ProbeRelPath: "src/opus_encoder.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt", "silk", "src"},
		DeadStrip:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name                  string
		rate, frameSize, seed int
		voiceEst              int32
	}{
		{"20ms", 48000, 960, 1, 102},
		{"40ms", 48000, 1920, 2, 115},
		{"5ms", 48000, 240, 3, 48},
		{"12k remainder", 12000, 30, 4, 80},
		{"96k", 96000, 1920, 5, 107},
	}
	const frames = 8
	payload := libopustest.NewOraclePayload("GFWI", uint32(len(cases)))
	inputs := make([][][]int32, len(cases))
	for c, tc := range cases {
		payload.U32(uint32(tc.rate))
		payload.U32(uint32(tc.frameSize))
		payload.U32(frames)
		payload.U32(uint32(tc.voiceEst))
		state := uint32(tc.seed)
		for range frames {
			pcm := make([]int32, 2*tc.frameSize)
			for i := 0; i < tc.frameSize; i++ {
				state = state*1664525 + 1013904223
				left := int32(int16(state>>16)) / 7
				state = state*1664525 + 1013904223
				noise := int32(int16(state>>16)) / 19
				pcm[2*i] = left*256 + int32(state&255)
				pcm[2*i+1] = (left*3/4+noise)*256 + int32((state>>8)&255)
			}
			inputs[c] = append(inputs[c], pcm)
			for _, v := range pcm {
				payload.U32(uint32(v))
			}
		}
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "fixed stereo width", "GFWO")
	if err != nil {
		t.Fatal(err)
	}
	if n := reader.Count(len(cases)); n != len(cases) {
		t.Fatalf("cases=%d want=%d", n, len(cases))
	}
	for c, tc := range cases {
		if n := reader.Count(frames); n != frames {
			t.Fatalf("%s frames=%d want=%d", tc.name, n, frames)
		}
		e := &Encoder{channels: 2, sampleRate: int32(tc.rate)}
		e.fixedInputActive = true
		for f, pcm := range inputs[c] {
			e.fixedRawRes = pcm
			gotWidth, ok := e.fixedStereoWidthForMode(tc.frameSize)
			if !ok {
				t.Fatalf("%s frame%d fixed estimator unavailable", tc.name, f)
			}
			gotThreshold, ok := e.fixedModeThreshold(tc.voiceEst)
			if !ok {
				t.Fatalf("%s frame%d fixed threshold unavailable", tc.name, f)
			}
			got := [7]int32{int32(e.fixedWidthQ15), e.fixedWidthMem.XX, e.fixedWidthMem.XY,
				e.fixedWidthMem.YY, int32(e.fixedWidthMem.SmoothedWidth),
				int32(e.fixedWidthMem.MaxFollower), gotThreshold}
			for field, value := range got {
				want := int32(reader.U32())
				if value != want {
					t.Fatalf("%s frame%d field%d Go=%d C=%d widthFloat=%g", tc.name, f, field, value, want, gotWidth)
				}
			}
		}
		lastPCM := inputs[c][frames-1]
		if n := testing.AllocsPerRun(100, func() {
			e.fixedWidthMem = fixedStereoWidthMem{}
			e.fixedRawRes = lastPCM
			e.fixedStereoWidthForMode(tc.frameSize)
			e.fixedModeThreshold(tc.voiceEst)
		}); n != 0 {
			t.Fatalf("%s allocations=%g want0", tc.name, n)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
