package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKPitchXcorrInputMagic  = "GSPX"
	libopusSILKPitchXcorrOutputMagic = "GSPY"
)

var libopusSILKPitchXcorrHelper libopustest.HelperCache

func getLibopusSILKPitchXcorrHelperPath() (string, error) {
	return libopusSILKPitchXcorrHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "silk pitch xcorr",
		OutputBase:   "gopus_libopus_silk_pitch_xcorr",
		SourceFile:   "libopus_silk_pitch_xcorr_info.c",
		ProbeRelPath: "celt/pitch.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O2", "-ffp-contract=off"},
		RefIncludes:  []string{"celt", "silk"},
		RefSources:   []string{"celt/pitch.c"},
		DeadStrip:    true,
	})
}

type libopusSILKPitchXcorrCase struct {
	name     string
	length   int
	maxPitch int
	x        []float32
	y        []float32
}

func probeLibopusSILKPitchXcorr(cases []libopusSILKPitchXcorrCase) ([][]float32, error) {
	binPath, err := getLibopusSILKPitchXcorrHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKPitchXcorrInputMagic, 0, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.length))
		payload.U32(uint32(tc.maxPitch))
		payload.Float32s(tc.x[:tc.length]...)
		payload.Float32s(tc.y[:tc.length+tc.maxPitch]...)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "silk pitch xcorr", libopusSILKPitchXcorrOutputMagic)
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	out := make([][]float32, count)
	for i := range out {
		maxPitch := int(reader.U32())
		if maxPitch <= 0 || maxPitch > 128 {
			return nil, fmt.Errorf("helper maxPitch=%d", maxPitch)
		}
		out[i] = make([]float32, maxPitch)
		for j := range out[i] {
			out[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestSILKPitchXcorrMatchesLibopusOracle(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusSILKPitchXcorrCase{
		{name: "short_tail", length: 7, maxPitch: 5, x: silkPitchXcorrOracleSignal(7, 0x11111111), y: silkPitchXcorrOracleSignal(12, 0x22222222)},
		{name: "unrolled", length: 32, maxPitch: 9, x: silkPitchXcorrOracleSignal(32, 0x33333333), y: silkPitchXcorrOracleSignal(41, 0x44444444)},
		{name: "pitch_frame", length: 120, maxPitch: 37, x: silkPitchXcorrOracleSignal(120, 0x55555555), y: silkPitchXcorrOracleSignal(157, 0x66666666)},
		{name: "odd_frame", length: 119, maxPitch: 31, x: silkPitchXcorrOracleSignal(119, 0x77777777), y: silkPitchXcorrOracleSignal(150, 0x88888888)},
	}
	want, err := probeLibopusSILKPitchXcorr(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "silk pitch xcorr", err)
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]float32, tc.maxPitch)
			celtPitchXcorrFloatImplScalar(tc.x, tc.y, got, tc.length, tc.maxPitch)
			if len(got) != len(want[i]) {
				t.Fatalf("xcorr len=%d want %d", len(got), len(want[i]))
			}
			for j := range got {
				if !pitchXcorrFloatMatches(got[j], want[i][j]) {
					t.Fatalf("xcorr[%d]=%08x %.10g want %08x %.10g",
						j,
						math.Float32bits(got[j]), got[j],
						math.Float32bits(want[i][j]), want[i][j])
				}
			}
		})
	}
}

func pitchXcorrFloatMatches(got, want float32) bool {
	// The scalar pitch-xcorr kernels route every product through noFMA32, so the
	// Go scalar path matches the -ffp-contract=off C oracle bit-for-bit on every
	// architecture; NEON dispatch exactness is covered by
	// TestSilkAssemblyKernelsMatchReference.
	return math.Float32bits(got) == math.Float32bits(want)
}

func silkPitchXcorrOracleSignal(n int, seed uint32) []float32 {
	x := make([]float32, n)
	state := seed
	for i := range x {
		state = state*1103515245 + 12345
		v := float32(int32(state>>9)&0x7fff-16384) * (1.0 / 16384.0)
		x[i] = v * float32(0.5+0.5*float32((i%5)+1)/5)
	}
	return x
}
