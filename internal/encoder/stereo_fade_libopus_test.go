package encoder

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestStereoFadeMatchesLibopus calls the actual static stereo_fade() in the
// selected libopus opus_encoder.c with the selected mode window. It checks
// every output bit across the overlap and the steady-width tail.
func TestStereoFadeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name                  string
		sampleRate, frameSize int
		prevWidth, nextWidth  int16
	}{
		{"48k_width_onset", 48000, 960, 16384, 16221},
		{"48k_width_ramp", 48000, 960, 14759, 14612},
		{"48k_width_steady", 48000, 960, 14907, 14907},
		{"48k_width_collapse", 48000, 960, 16384, 0},
		{"48k_minimum_frame", 48000, 120, 16384, 14000},
		{"24k_width_ramp", 24000, 480, 14759, 14612},
		{"24k_minimum_frame", 24000, 60, 16384, 8192},
		{"48k_zero_width", 48000, 960, 0, 0},
		{"48k_half_width", 48000, 960, 8192, 4096},
	}
	inputs := make([][]opusRes, len(cases))
	payload := libopustest.NewOraclePayload("GTFI", uint32(len(cases)))
	for i, tc := range cases {
		input := make([]opusRes, 2*tc.frameSize)
		for j := range tc.frameSize {
			left := float32(.45*math.Sin(float64(j)*.19) + .25*math.Cos(float64(j)*.071))
			right := float32(.3*math.Cos(float64(j)*.113) - .18*math.Sin(float64(j)*.047))
			if j%13 == 0 {
				right = left * (1 - 0x1p-18)
			}
			input[2*j], input[2*j+1] = left, right
		}
		inputs[i] = input
		payload.U32(uint32(tc.sampleRate))
		payload.U32(uint32(tc.frameSize))
		payload.U32(uint32(tc.prevWidth))
		payload.U32(uint32(tc.nextWidth))
		payload.U32(uint32(len(input)))
		payload.Float32s(input...)
	}
	bin, err := libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "libopus stereo fade",
		OutputBase:  "gopus_libopus_stereo_fade",
		SourceFile:  "libopus_stereo_fade_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo fade", err)
		return
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "stereo fade", "GTFO")
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo fade", err)
		return
	}
	if count := reader.Count(len(cases)); count != len(cases) {
		t.Fatalf("C case count=%d want=%d: %v", count, len(cases), reader.Err())
	}
	for i, tc := range cases {
		count := int(reader.U32())
		if count != len(inputs[i]) {
			t.Fatalf("%s: C sample count=%d want=%d: %v", tc.name, count, len(inputs[i]), reader.Err())
		}
		want := make([]float32, count)
		for j := range want {
			want[j] = reader.Float32()
		}
		if err := reader.Err(); err != nil {
			t.Fatal(err)
		}
		t.Run(tc.name, func(t *testing.T) {
			e := &Encoder{channels: 2, sampleRate: int32(tc.sampleRate)}
			got := append([]opusRes(nil), inputs[i]...)
			e.applyStereoFade(got, tc.prevWidth, tc.nextWidth)
			for j := range got {
				if gotBits, wantBits := math.Float32bits(float32(got[j])), math.Float32bits(want[j]); gotBits != wantBits {
					t.Errorf("sample %d: Go=%08x C=%08x", j, gotBits, wantBits)
					break
				}
			}
			copy(got, inputs[i])
			e.applyStereoFade(got, tc.prevWidth, tc.nextWidth)
			if allocs := testing.AllocsPerRun(100, func() {
				copy(got, inputs[i])
				e.applyStereoFade(got, tc.prevWidth, tc.nextWidth)
			}); allocs != 0 {
				t.Errorf("steady-state allocations=%s want=0", fmt.Sprint(allocs))
			}
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
