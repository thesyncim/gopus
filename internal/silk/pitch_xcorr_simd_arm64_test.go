//go:build arm64 && goexperiment.simd && !nosimd

package silk

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusSILKPitchXcorrNEONInputMagic  = "GSPN"
	libopusSILKPitchXcorrNEONOutputMagic = "GSPQ"
)

var libopusSILKPitchXcorrNEONHelper libopustest.HelperCache

func buildLibopusSILKPitchXcorrNEONHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:        "SILK pitch xcorr NEON",
		OutputBase:   "gopus_libopus_silk_pitch_xcorr_neon",
		SourceFile:   "libopus_silk_pitch_xcorr_neon_info.c",
		ProbeRelPath: "celt/arm/celt_neon_intr.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O2", "-ffp-contract=off"},
		RefIncludes:  []string{"celt", "celt/arm"},
		SIMDRef:      true,
		RefSources: []string{
			"celt/arm/celt_neon_intr.c",
			"celt/arm/pitch_neon_intr.c",
		},
		Libs:      []string{"-lm"},
		DeadStrip: true,
	})
}

func probeLibopusSILKPitchXcorrNEON(cases []libopusSILKPitchXcorrCase) ([][]float32, error) {
	binPath, err := libopusSILKPitchXcorrNEONHelper.Path(buildLibopusSILKPitchXcorrNEONHelper)
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload(libopusSILKPitchXcorrNEONInputMagic, 0, uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.length))
		payload.U32(uint32(tc.maxPitch))
		payload.Float32s(tc.x[:tc.length]...)
		payload.Float32s(tc.y[:tc.length+tc.maxPitch-1]...)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "SILK pitch xcorr NEON", libopusSILKPitchXcorrNEONOutputMagic)
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

func TestSILKPitchXcorrARM64SIMDMatchesLibopusNEON(t *testing.T) {
	libopustest.RequireOracle(t)
	if !usePitchXcorrArm64SIMD {
		t.Fatal("ARM64 SIMD build does not select the SIMD pitch xcorr implementation")
	}
	offsetX := silkPitchXcorrOracleSignal(241, 0xbbbbbbbb)
	offsetY := silkPitchXcorrOracleSignal(360, 0xcccccccc)
	exceptionX := silkPitchXcorrOracleSignal(9, 0x13579bdf)
	exceptionY := silkPitchXcorrOracleSignal(15, 0x2468ace0)
	exceptionX[0] = math.Float32frombits(0x7fc01234)
	exceptionX[1] = float32(math.Inf(1))
	exceptionX[2] = float32(math.Inf(-1))
	exceptionX[3] = 0
	exceptionX[4] = math.Float32frombits(0x80000000)
	exceptionY[0] = 0
	exceptionY[1] = math.Float32frombits(0x7fc05678)
	exceptionY[2] = float32(math.Inf(1))
	exceptionY[3] = float32(math.Inf(-1))
	exceptionY[4] = math.Float32frombits(0x00000001)
	cases := []libopusSILKPitchXcorrCase{
		{name: "short", length: 7, maxPitch: 4, x: silkPitchXcorrOracleSignal(7, 0x11111111), y: silkPitchXcorrOracleSignal(10, 0x22222222)},
		{name: "unrolled", length: 32, maxPitch: 8, x: silkPitchXcorrOracleSignal(32, 0x33333333), y: silkPitchXcorrOracleSignal(39, 0x44444444)},
		{name: "pitch_frame", length: 120, maxPitch: 36, x: silkPitchXcorrOracleSignal(120, 0x55555555), y: silkPitchXcorrOracleSignal(155, 0x66666666)},
		{name: "production", length: 240, maxPitch: 120, x: silkPitchXcorrOracleSignal(240, 0x77777777), y: silkPitchXcorrOracleSignal(359, 0x88888888)},
		{name: "long_odd_length", length: 239, maxPitch: 32, x: silkPitchXcorrOracleSignal(239, 0x99999999), y: silkPitchXcorrOracleSignal(270, 0xaaaaaaaa)},
		{name: "production_offset_exact_tail", length: 240, maxPitch: 120, x: offsetX[1:241], y: offsetY[1:360]},
		{name: "tail_one_pitch", length: 31, maxPitch: 1, x: silkPitchXcorrOracleSignal(31, 0xbbbbbbbb), y: silkPitchXcorrOracleSignal(31, 0xcccccccc)},
		{name: "tail_three_pitches", length: 33, maxPitch: 3, x: silkPitchXcorrOracleSignal(33, 0xdddddddd), y: silkPitchXcorrOracleSignal(35, 0xeeeeeeee)},
		{name: "tail_seven_pitches", length: 17, maxPitch: 7, x: silkPitchXcorrOracleSignal(17, 0xfedcba98), y: silkPitchXcorrOracleSignal(23, 0x12345678)},
		{name: "tail_exceptional_values", length: 9, maxPitch: 7, x: exceptionX, y: exceptionY},
	}
	want, err := probeLibopusSILKPitchXcorrNEON(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK pitch xcorr NEON", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStorage := make([]float32, tc.maxPitch+2)
			gotStorage[0], gotStorage[tc.maxPitch+1] = 123, -123
			got := gotStorage[1 : tc.maxPitch+1]
			celtPitchXcorrFloat(tc.x, tc.y, got, tc.length, tc.maxPitch)
			for j := range got {
				if math.Float32bits(got[j]) != math.Float32bits(want[i][j]) {
					t.Fatalf("xcorr[%d]=%08x %.10g want %08x %.10g", j, math.Float32bits(got[j]), got[j], math.Float32bits(want[i][j]), want[i][j])
				}
			}
			if gotStorage[0] != 123 || gotStorage[tc.maxPitch+1] != -123 {
				t.Fatalf("xcorr wrote beyond output: guards %g, %g", gotStorage[0], gotStorage[tc.maxPitch+1])
			}
		})
	}
}

func TestSILKPitchXcorrARM64SIMDBoundsAndTail(t *testing.T) {
	libopustest.RequireOracle(t)
	const length, maxPitch = 17, 7
	xStorage := silkPitchXcorrOracleSignal(length+1, 0xbbbbbbbb)
	yStorage := silkPitchXcorrOracleSignal(length+maxPitch+2, 0xcccccccc)
	outStorage := make([]float32, maxPitch+2)
	x := xStorage[1:]
	y := yStorage[1 : length+maxPitch]
	out := outStorage[1 : maxPitch+1]
	outStorage[0], outStorage[maxPitch+1] = 123, -123
	want, err := probeLibopusSILKPitchXcorrNEON([]libopusSILKPitchXcorrCase{{
		name: "offset_tail", length: length, maxPitch: maxPitch, x: x, y: y,
	}})
	if err != nil {
		libopustest.HelperUnavailable(t, "SILK pitch xcorr NEON", err)
	}
	celtPitchXcorrFloat(x, y, out, length, maxPitch)
	for i := range want[0] {
		if math.Float32bits(out[i]) != math.Float32bits(want[0][i]) {
			t.Fatalf("tail xcorr[%d]=%08x want %08x", i, math.Float32bits(out[i]), math.Float32bits(want[0][i]))
		}
	}
	if outStorage[0] != 123 || outStorage[maxPitch+1] != -123 {
		t.Fatalf("xcorr wrote beyond output: guards %g, %g", outStorage[0], outStorage[maxPitch+1])
	}
}

func TestSILKPitchXcorrARM64SIMDAllocatesZero(t *testing.T) {
	const length, maxPitch = 240, 120
	x := silkPitchXcorrOracleSignal(length, 0xdddddddd)
	y := silkPitchXcorrOracleSignal(length+maxPitch-1, 0xeeeeeeee)
	out := make([]float32, maxPitch)
	celtPitchXcorrFloatImpl(x, y, out, length, maxPitch)
	allocs := testing.AllocsPerRun(100, func() {
		celtPitchXcorrFloatImpl(x, y, out, length, maxPitch)
		silkPitchXcorrSIMDAllocSink = out[maxPitch-1]
	})
	if allocs != 0 {
		t.Fatalf("pitch xcorr allocated %g times per run", allocs)
	}
}

var silkPitchXcorrSIMDAllocSink float32

func BenchmarkSILKPitchXcorrARM64SIMD(b *testing.B) {
	const length, maxPitch = 240, 120
	x := silkPitchXcorrOracleSignal(length, 0xfedcba98)
	y := silkPitchXcorrOracleSignal(length+maxPitch-1, 0x12345678)
	out := make([]float32, maxPitch)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		celtPitchXcorrFloatImpl(x, y, out, length, maxPitch)
	}
	b.StopTimer()
	silkPitchXcorrSIMDAllocSink = out[maxPitch-1]
}

func BenchmarkSILKPitchXcorrARM64Scalar(b *testing.B) {
	const length, maxPitch = 240, 120
	x := silkPitchXcorrOracleSignal(length, 0xfedcba98)
	y := silkPitchXcorrOracleSignal(length+maxPitch-1, 0x12345678)
	out := make([]float32, maxPitch)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		celtPitchXcorrFloatImplScalar(x, y, out, length, maxPitch)
	}
	b.StopTimer()
	silkPitchXcorrSIMDAllocSink = out[maxPitch-1]
}
