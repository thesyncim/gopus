//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"os"
	"testing"
)

func xcorrKernelAVX8SplitForTest(x, y []float32, sum *[8]float32, length int) {
	xcorrKernelAVX4(x, y, (*[4]float32)(sum[:4]), length)
	xcorrKernelAVX4(x, y[4:], (*[4]float32)(sum[4:]), length)
}

func TestSilkXcorrKernelAVX8OnePassBitExact(t *testing.T) {
	if !silkUsePitchXcorrAVX2FMA {
		t.Skip("AVX2/FMA unavailable")
	}
	rng := rand.New(rand.NewSource(0x8acc))
	for _, length := range []int{1, 7, 8, 9, 15, 16, 17, 23, 31, 32, 64, 65, 119, 120, 127, 128, 239, 240, 241, 479, 480, 481} {
		x := make([]float32, length)
		y := make([]float32, length+7)
		for trial := 0; trial < 32; trial++ {
			for i := range x {
				x[i] = float32(rng.NormFloat64())
			}
			for i := range y {
				y[i] = float32(rng.NormFloat64())
			}
			want := xcorrKernelAVX8Reference(x, y, length)
			var split, onePass [8]float32
			xcorrKernelAVX8SplitForTest(x, y, &split, length)
			xcorrKernelAVX8OnePass(x, y, &onePass, length)
			for corr := range 8 {
				if got, expected := math.Float32bits(onePass[corr]), math.Float32bits(want[corr]); got != expected {
					t.Fatalf("length=%d trial=%d corr=%d: one-pass=%08x scalar=%08x", length, trial, corr, got, expected)
				}
				if got, expected := math.Float32bits(onePass[corr]), math.Float32bits(split[corr]); got != expected {
					t.Fatalf("length=%d trial=%d corr=%d: one-pass=%08x split=%08x", length, trial, corr, got, expected)
				}
			}
		}
	}
}

func TestSilkXcorrKernelAVX8OnePassExceptionalParityAndZeroAlloc(t *testing.T) {
	if !silkUsePitchXcorrAVX2FMA {
		t.Skip("AVX2/FMA unavailable")
	}
	values := []float32{
		0, math.Float32frombits(1 << 31), math.SmallestNonzeroFloat32,
		-math.SmallestNonzeroFloat32, 0.5, -0.5, 1, -1,
		float32(math.Inf(1)), float32(math.Inf(-1)), math.Float32frombits(0x7fc01234),
	}
	for _, length := range []int{17, 31, 240, 241} {
		x := make([]float32, length)
		y := make([]float32, length+7)
		for i := range x {
			x[i] = values[(i*5+1)%len(values)]
		}
		for i := range y {
			y[i] = values[(i*7+3)%len(values)]
		}
		var split, onePass [8]float32
		xcorrKernelAVX8SplitForTest(x, y, &split, length)
		xcorrKernelAVX8OnePass(x, y, &onePass, length)
		for corr := range 8 {
			if got, expected := math.Float32bits(onePass[corr]), math.Float32bits(split[corr]); got != expected {
				t.Fatalf("length=%d corr=%d: one-pass=%08x split=%08x", length, corr, got, expected)
			}
		}
	}

	const length = 240
	x := make([]float32, length)
	y := make([]float32, length+7)
	for i := range x {
		x[i] = float32(i%13-6) * 0.03125
	}
	for i := range y {
		y[i] = float32(i%17-8) * 0.0625
	}
	var sum [8]float32
	xcorrKernelAVX8OnePass(x, y, &sum, length)
	if allocs := testing.AllocsPerRun(100, func() {
		xcorrKernelAVX8OnePass(x, y, &sum, length)
	}); allocs != 0 {
		t.Fatalf("one-pass xcorr allocated %v times", allocs)
	}
}

func BenchmarkXcorrKernelAVX8Passes(b *testing.B) {
	for _, length := range []int{120, 240, 480} {
		x := make([]float32, length)
		y := make([]float32, length+7)
		for i := range x {
			x[i] = float32(i%29-14) * 0.03125
		}
		for i := range y {
			y[i] = float32(i%23-11) * 0.0625
		}
		b.Run(fmt.Sprintf("N%d/Split", length), func(b *testing.B) {
			var sum [8]float32
			b.ReportAllocs()
			for b.Loop() {
				xcorrKernelAVX8SplitForTest(x, y, &sum, length)
				xcorrKernelAVX8BenchmarkSink = sum
			}
		})
		b.Run(fmt.Sprintf("N%d/OnePass", length), func(b *testing.B) {
			var sum [8]float32
			b.ReportAllocs()
			for b.Loop() {
				xcorrKernelAVX8OnePass(x, y, &sum, length)
				xcorrKernelAVX8BenchmarkSink = sum
			}
		})
	}
}

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
	runSilkPitchXcorrSIMDOracle(t, silkPitchXcorrSIMDCases())
}

// TestSILKPitchXcorrMatchesLibopusOracle runs the shared SILK pitch xcorr
// cases against the paired libopus SIMD archive; the scalar lane runs the same
// cases against the compiler-matched scalar helper.
func TestSILKPitchXcorrMatchesLibopusOracle(t *testing.T) {
	shared := silkPitchXcorrLibopusCases()
	cases := make([]silkPitchXcorrSIMDCase, len(shared))
	for i, tc := range shared {
		cases[i] = silkPitchXcorrSIMDCase{name: tc.name, x: tc.x[:tc.length], y: tc.y[:tc.length+tc.maxPitch-1], maxPitch: tc.maxPitch}
	}
	runSilkPitchXcorrSIMDOracle(t, cases)
}

func runSilkPitchXcorrSIMDOracle(t *testing.T, cases []silkPitchXcorrSIMDCase) {
	t.Helper()
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

	payload := libopustest.NewOraclePayload("GXCI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.x)))
		payload.U32(uint32(tc.maxPitch))
		payload.Float32s(tc.x...)
		payload.Float32s(tc.y...)
	}
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "SILK paired native SIMD pitch xcorr", "GXCO", 3)
	if err != nil {
		if requireNative {
			t.Fatalf("run required SILK paired native SIMD xcorr oracle: %v", err)
		}
		libopustest.HelperUnavailable(t, "SILK paired native SIMD pitch xcorr", err)
		return
	}
	arch, cpu, dispatch, presumed := reader.U32(), reader.U32(), reader.U32(), reader.U32()
	if !libopustest.NativeX86PitchXCorrMetadataValid(arch, cpu, dispatch, presumed) {
		t.Fatalf("paired libopus did not select AVX2/FMA xcorr and SSE remainder: arch=%d cpu=%03b dispatch=%02b presumed=%02b", arch, cpu, dispatch, presumed)
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
			if tc.maxPitch < 8 {
				// The 8-lane kernels read eight correlations past each x sample.
				return
			}
			var direct, split, onePass [8]float32
			xcorrKernelAVX8(tc.x, tc.y, &direct, len(tc.x))
			xcorrKernelAVX8SplitForTest(tc.x, tc.y, &split, len(tc.x))
			xcorrKernelAVX8OnePass(tc.x, tc.y, &onePass, len(tc.x))
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

func silkPitchXcorrSIMDCases() []silkPitchXcorrSIMDCase {
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

	return cases
}

// NaN payload selection is defined by the selected C instructions, so the
// exceptional-value oracle must execute libopus rather than Go math.FMA.
func TestSilkPitchXcorrAVX2TinyFirstLaneEdgeValues(t *testing.T) {
	values := []float32{
		0, math.Float32frombits(1 << 31),
		math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32,
		0.5, -0.5, 1, -1,
		float32(math.Inf(1)), float32(math.Inf(-1)), math.Float32frombits(0x7fc01234),
	}
	var cases []silkPitchXcorrSIMDCase
	for _, length := range []int{1, 5, 8, 9, 10, 15} {
		x := make([]float32, length)
		y := make([]float32, length+7)
		for i := range x {
			x[i] = values[i%len(values)]
		}
		for i := range y {
			y[i] = values[(i*3+1)%len(values)]
		}
		cases = append(cases, silkPitchXcorrSIMDCase{name: fmt.Sprintf("tiny_first_lane_N%d", length), x: x, y: y, maxPitch: 8})
	}
	runSilkPitchXcorrSIMDOracle(t, cases)
	x := []float32{1, 2, 3, 4, 5}
	y := []float32{5, 4, 3, 2, 1, 0, -1, -2, -3, -4, -5, -6}
	var sum [8]float32
	xcorrKernelAVX8(x, y, &sum, len(x))
	if allocs := testing.AllocsPerRun(100, func() {
		xcorrKernelAVX8(x, y, &sum, len(x))
	}); allocs != 0 {
		t.Fatalf("tiny SILK xcorr kernel allocated %v times", allocs)
	}
}

func sseOrderInnerProductRef(x, y []float32, n int) float32 {
	var acc [4]float32
	i := 0
	for ; i+4 <= n; i += 4 {
		for l := range 4 {
			acc[l] += noFMA32(x[i+l], y[i+l])
		}
	}
	sum := (acc[0] + acc[2]) + (acc[1] + acc[3])
	for ; i < n; i++ {
		sum += noFMA32(x[i], y[i])
	}
	return sum
}

// TestCeltPitchXcorrAVX2RemainderUsesSSEOrder pins the lags past the last
// eight-lag AVX2 block to celt_inner_prod_sse, which celt_pitch_xcorr_avx2
// calls through the x86 dispatch table.
func TestCeltPitchXcorrAVX2RemainderUsesSSEOrder(t *testing.T) {
	if !silkUsePitchXcorrAVX2FMA {
		t.Skip("AVX2+FMA pitch xcorr is not selected on this CPU")
	}
	rng := rand.New(rand.NewSource(9))
	for _, length := range []int{5, 40, 80, 81, 120} {
		for _, maxPitch := range []int{3, 11, 17, 22, 149} {
			x := make([]float32, length)
			y := make([]float32, length+maxPitch+8)
			for i := range x {
				x[i] = rng.Float32()*2 - 1
			}
			for i := range y {
				y[i] = rng.Float32()*2 - 1
			}
			out := make([]float32, maxPitch)
			celtPitchXcorrFloat(x, y, out, length, maxPitch)
			for i := maxPitch &^ 7; i < maxPitch; i++ {
				want := sseOrderInnerProductRef(x, y[i:], length)
				if math.Float32bits(out[i]) != math.Float32bits(want) {
					t.Fatalf("len=%d maxPitch=%d lag %d = %08x, want %08x", length, maxPitch, i,
						math.Float32bits(out[i]), math.Float32bits(want))
				}
			}
		}
	}
}
