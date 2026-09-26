//go:build amd64 && goexperiment.simd && !nosimd

package silk

import (
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var silkPitchXcorrSIMDHelper libopustest.HelperCache

type silkPitchXcorrSIMDCase struct {
	name     string
	x, y     []float32
	maxPitch int
}

// The SILK pitch search calls the same celt_pitch_xcorr entry point as CELT.
// This oracle links libopus's actual SIMD archive and checks its selected
// AVX2/FMA xcorr and SSE remainder dispatch before comparing raw float bits.
func TestSilkPitchXCorrPairedLibopusSIMDRawBits(t *testing.T) {
	requireNative := os.Getenv("GOPUS_REQUIRE_NATIVE_AVX2_FMA") == "1"
	if requireNative && !libopustest.OracleEnabled() {
		t.Fatal("native SIMD CI requires the libopus oracle")
	}
	libopustest.RequireOracle(t)
	if !silkUsePitchXcorrAVX2FMA {
		if requireNative || libopustest.StrictRefRequired() {
			t.Fatal("native SIMD CI requires SILK AVX2/FMA xcorr dispatch")
		}
		t.Skip("SILK AVX2/FMA xcorr dispatch is unavailable on this host")
	}
	cflags := []string{"-DHAVE_CONFIG_H"}
	if requireNative {
		cflags = append(cflags, "-DGOPUS_REQUIRE_NATIVE_AVX2_FMA=1")
	}
	binPath, err := silkPitchXcorrSIMDHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "SILK paired native SIMD pitch xcorr",
		OutputBase:   "gopus_libopus_silk_pitch_xcorr_simd",
		SourceFile:   "libopus_celt_pitch_xcorr_simd_info.c",
		ProbeRelPath: "config.h",
		CFlags:       cflags,
		RefIncludes:  []string{"celt"},
		SIMDRef:      true,
		Libs:         []string{libopustest.SIMDRefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		if requireNative {
			t.Fatalf("build required SILK paired native SIMD xcorr oracle: %v", err)
		}
		libopustest.HelperUnavailable(t, "SILK paired native SIMD pitch xcorr", err)
		return
	}

	cases := []silkPitchXcorrSIMDCase{
		{name: "finite17", x: silkPitchXcorrOracleSignal(17, 0x15555555), y: silkPitchXcorrOracleSignal(26, 0x27777777), maxPitch: 10},
		{name: "short_fma32_rounding", x: make([]float32, 9), y: make([]float32, 16), maxPitch: 8},
		{name: "sse10_nan_operand_priority", x: make([]float32, 10), y: make([]float32, 19), maxPitch: 10},
		{name: "distinct_nan_payloads17", x: make([]float32, 17), y: make([]float32, 26), maxPitch: 10},
		{name: "masked_negative_zero17", x: make([]float32, 17), y: make([]float32, 26), maxPitch: 10},
		{name: "exceptional31", x: make([]float32, 31), y: make([]float32, 40), maxPitch: 10},
	}
	cases[1].x[0], cases[1].y[0] = math.Float32frombits(0xa20c2545), 1
	cases[1].x[8], cases[1].y[8] = math.Float32frombits(0x3fcca800), math.Float32frombits(0x3f979800)
	cases[2].x[1], cases[2].y[10] = math.Float32frombits(0x80000000), float32(math.Inf(-1))
	cases[2].x[5], cases[2].y[14] = -0.5, math.Float32frombits(0x7fc01234)
	for i := range cases[3].x {
		cases[3].x[i] = float32(i%7-3) * 0.25
	}
	for i := range cases[3].y {
		cases[3].y[i] = float32(i%5-2) * 0.5
	}
	cases[3].x[3] = math.Float32frombits(0x7fc01234)
	cases[3].y[8] = math.Float32frombits(0xffc05678)
	for i := range cases[4].x {
		cases[4].x[i] = math.Float32frombits(0x80000001)
	}
	for i := range cases[4].y {
		cases[4].y[i] = math.Float32frombits(0x00000001)
	}
	values := []float32{
		0, math.Float32frombits(0x80000000), math.SmallestNonzeroFloat32,
		-math.SmallestNonzeroFloat32, 0.5, -0.5, 1, -1,
		float32(math.Inf(1)), float32(math.Inf(-1)), math.Float32frombits(0x7fc01234),
	}
	for i := range cases[5].x {
		cases[5].x[i] = values[(i*5+1)%len(values)]
	}
	for i := range cases[5].y {
		cases[5].y[i] = values[(i*7+3)%len(values)]
	}

	payload := libopustest.NewOraclePayload("GXCI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.maxPitch))
		payload.Float32s(tc.x...)
		payload.Float32s(tc.y...)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "SILK paired native SIMD pitch xcorr", "GXCO", 2)
	if err != nil {
		if requireNative {
			t.Fatalf("run required SILK paired native SIMD xcorr oracle: %v", err)
		}
		libopustest.HelperUnavailable(t, "SILK paired native SIMD pitch xcorr", err)
		return
	}
	arch, cpu, dispatch := reader.U32(), reader.U32(), reader.U32()
	if arch < 4 || cpu&6 != 6 || dispatch&3 != 3 {
		t.Fatalf("paired libopus did not select AVX2/FMA xcorr and SSE remainder: arch=%d cpu=%03b dispatch=%02b", arch, cpu, dispatch)
	}
	if got := reader.Count(len(cases)); got != len(cases) {
		t.Fatalf("SILK paired xcorr records=%d want %d", got, len(cases))
	}
	for _, tc := range cases {
		wantLen := int(reader.U32())
		if wantLen != tc.maxPitch {
			t.Fatalf("%s C output length=%d want %d", tc.name, wantLen, tc.maxPitch)
		}
		want := make([]float32, wantLen)
		for j := range want {
			want[j] = reader.Float32()
		}
		t.Run(tc.name, func(t *testing.T) {
			got := make([]float32, tc.maxPitch)
			celtPitchXcorrFloatImpl(tc.x, tc.y, got, len(tc.x), tc.maxPitch)
			t.Run("production", func(t *testing.T) { assertSilkPitchXcorrRawBits(t, got, want) })
			var direct, split, onePass [8]float32
			xcorrKernelAVX8(&tc.x[0], &tc.y[0], &direct, len(tc.x))
			xcorrKernelAVX8SplitForTest(&tc.x[0], &tc.y[0], &split, len(tc.x))
			xcorrKernelAVX8OnePass(&tc.x[0], &tc.y[0], &onePass, len(tc.x))
			t.Run("direct8", func(t *testing.T) { assertSilkPitchXcorrRawBits(t, direct[:], want[:8]) })
			t.Run("split8", func(t *testing.T) { assertSilkPitchXcorrRawBits(t, split[:], want[:8]) })
			t.Run("onepass8", func(t *testing.T) { assertSilkPitchXcorrRawBits(t, onePass[:], want[:8]) })
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}

func assertSilkPitchXcorrRawBits(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("length=%d want %d", len(got), len(want))
		return
	}
	for i := range got {
		if a, b := math.Float32bits(got[i]), math.Float32bits(want[i]); a != b {
			t.Errorf("first difference at correlation %d: got=%08x want=%08x", i, a, b)
			return
		}
	}
}

func TestSilkPitchXcorrNativeZeroAlloc(t *testing.T) {
	if !silkUsePitchXcorrAVX2FMA {
		if os.Getenv("GOPUS_REQUIRE_NATIVE_AVX2_FMA") == "1" || libopustest.StrictRefRequired() {
			t.Fatal("native SIMD CI requires SILK AVX2/FMA xcorr dispatch")
		}
		t.Skip("AVX2/FMA unavailable")
	}
	for _, length := range []int{10, 17, 120} {
		x := silkPitchXcorrOracleSignal(length, uint32(length))
		y := silkPitchXcorrOracleSignal(length+9, uint32(length+9))
		out := make([]float32, 10)
		celtPitchXcorrFloatImpl(x, y, out, length, 10)
		if allocs := testing.AllocsPerRun(100, func() {
			celtPitchXcorrFloatImpl(x, y, out, length, 10)
		}); allocs != 0 {
			t.Errorf("length=%d production xcorr allocated %v times", length, allocs)
		}
	}
	x := make([]float32, 17)
	y := make([]float32, 26)
	out := make([]float32, 10)
	x[0], y[0] = 0, float32(math.Inf(-1))
	x[3] = math.Float32frombits(0x7fc01234)
	y[8] = math.Float32frombits(0xffc05678)
	celtPitchXcorrFloatImpl(x, y, out, len(x), len(out))
	if allocs := testing.AllocsPerRun(100, func() {
		celtPitchXcorrFloatImpl(x, y, out, len(x), len(out))
	}); allocs != 0 {
		t.Errorf("exceptional production xcorr allocated %v times", allocs)
	}
}
