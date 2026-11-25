package encoder

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

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

func TestStereoWidthComputation(t *testing.T) {
	libopustest.RequireOracle(t)
	const frames = 8
	cases := []struct {
		name          string
		phase, gain   float64
		quiet, seeded bool
		rate, n       int
	}{
		{"mono signal", 0, 1, false, false, 48000, 960},
		{"full stereo (90deg)", math.Pi / 2, 1, false, false, 48000, 960},
		{"opposite phase", math.Pi, 1, false, false, 48000, 960},
		{"slight stereo", .1, 1, false, false, 48000, 960},
		{"unequal energy", .8, .35, false, false, 48000, 120},
		{"quiet after active", .8, .35, true, false, 48000, 480},
		{"seeded state", .3, .6, false, true, 48000, 960},
		{"12k remainder", .3, .6, false, false, 12000, 30},
		{"40ms", .3, .6, false, true, 48000, 1920},
		{"60ms", .3, .6, false, true, 48000, 2880},
		{"120ms", .3, .6, false, true, 48000, 5760},
	}
	initial := make([]StereoWidthMem, len(cases))
	inputs := make([][][]opusRes, len(cases))
	payload := libopustest.NewOraclePayload("GSWI", uint32(len(cases)))
	for i, tc := range cases {
		if tc.seeded {
			initial[i] = StereoWidthMem{XX: .125, XY: .08, YY: .25, SmoothedWidth: .002, MaxFollower: .1}
		}
		m := initial[i]
		payload.U32(uint32(tc.rate))
		payload.U32(uint32(tc.n))
		payload.U32(frames)
		payload.Float32s(m.XX, m.XY, m.YY, m.SmoothedWidth, m.MaxFollower)
		for f := range frames {
			pcm := make([]opusRes, 2*tc.n)
			for j := range tc.n {
				phase := 2 * math.Pi * 440 * float64(f*tc.n+j) / float64(tc.rate)
				pcm[2*j] = float32(.5 * math.Sin(phase))
				pcm[2*j+1] = float32(.5 * tc.gain * math.Sin(phase+tc.phase))
				if tc.quiet && f >= frames/2 {
					pcm[2*j] = 0
					pcm[2*j+1] = 0
				}
			}
			inputs[i] = append(inputs[i], pcm)
			payload.Float32s(pcm...)
		}
	}
	bin, err := libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label: "libopus stateful stereo width", OutputBase: "gopus_libopus_stereo_width", SourceFile: "libopus_stereo_width_info.c",
		CFlags: []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"}, RefIncludes: []string{"celt", "silk", "src"},
		Libs: []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"}, DeadStrip: true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo width", err)
		return
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "stereo width", "GSWO")
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo width", err)
		return
	}
	if n := reader.Count(len(cases)); n != len(cases) {
		t.Fatalf("C cases=%d want=%d", n, len(cases))
	}
	for i, tc := range cases {
		if n := reader.U32(); n != frames {
			t.Fatalf("C frames=%d want=%d", n, frames)
		}
		var expected [frames][6]float32
		for f := range expected {
			for k := range expected[f] {
				expected[f][k] = reader.Float32()
			}
		}
		if err := reader.Err(); err != nil {
			t.Fatal(err)
		}
		t.Run(tc.name, func(t *testing.T) {
			e := &Encoder{channels: 2, sampleRate: int32(tc.rate), widthMem: initial[i]}
			for f, pcm := range inputs[i] {
				width := e.computeStereoWidthForMode(pcm, tc.n)
				m := e.widthMem
				got := [6]float32{width, m.XX, m.XY, m.YY, m.SmoothedWidth, m.MaxFollower}
				for k, v := range got {
					if math.Float32bits(v) != math.Float32bits(expected[f][k]) {
						t.Fatalf("frame%d field%d bits: Go=%08x C=%08x", f, k, math.Float32bits(v), math.Float32bits(expected[f][k]))
					}
				}
			}
			e.widthMem = initial[i]
			e.computeStereoWidthForMode(inputs[i][0], tc.n)
			if n := testing.AllocsPerRun(100, func() { e.widthMem = initial[i]; e.computeStereoWidthForMode(inputs[i][0], tc.n) }); n != 0 {
				t.Fatalf("allocations=%v want0", n)
			}
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
