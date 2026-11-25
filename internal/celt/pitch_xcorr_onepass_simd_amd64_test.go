//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
	"math"
	"math/rand"
	"os"
	"simd/archsimd"
	"strings"
	"testing"
)

func xcorrKernelAVX4(x, y []float32, sum *[4]float32, length int) {
	tail := newXcorrTail8(x, length)
	xcorrKernelAVX4Tail(x, y, sum, length, &tail)
}

func xcorrKernelAVX4Tail(x, y []float32, sum *[4]float32, length int, tail *xcorrTail8) {
	var acc0, acc1, acc2, acc3 archsimd.Float32x8
	x = x[:length]
	y = y[:length+3]
	i := 0
	for ; i+16 <= length; i += 16 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(x[i:]))
		yb := y[i : i+19]
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[0:])), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[1:])), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[2:])), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[3:])), acc3)
		xv = archsimd.LoadFloat32x8Array((*[8]float32)(x[i+8:]))
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[8:])), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[9:])), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[10:])), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[11:])), acc3)
	}
	for ; i+8 <= length; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(x[i:]))
		yb := y[i : i+11]
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[0:])), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[1:])), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[2:])), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[3:])), acc3)
	}
	if i < length {
		xTail := tail.x
		yb := y[i:]
		acc0 = xTail.MulAdd(loadXcorrTail8(yb[0:], tail.rem), acc0)
		acc1 = xTail.MulAdd(loadXcorrTail8(yb[1:], tail.rem), acc1)
		acc2 = xTail.MulAdd(loadXcorrTail8(yb[2:], tail.rem), acc2)
		acc3 = xTail.MulAdd(loadXcorrTail8(yb[3:], tail.rem), acc3)
	}
	reduceXcorrAVX8Four(acc0, acc1, acc2, acc3).StoreArray(sum)
	// Clear before the legacy-SSE NaN checks; the outer wrapper clears after this block.
	archsimd.ClearAVXUpperBits()
	for corr := range sum {
		if sum[corr] != sum[corr] {
			sum[corr] = opusmath.PitchXcorrAVX2NaNReplay(x, y[corr:], length)
		}
	}
}

// reduceXcorrAVX8Lanes leaves the exact horizontal sum broadcast in all lanes.
func reduceXcorrAVX8Lanes(v archsimd.Float32x8) archsimd.Float32x8 {
	v = v.Add(v.ConcatPermute128Scalars(1, 0, v))
	v = v.ConcatAddPairsGrouped(v)
	v = v.ConcatAddPairsGrouped(v)
	return v
}

// reduceXcorrAVX8Four packs four lane-zero sums with three vector shuffles.
func reduceXcorrAVX8Four(v0, v1, v2, v3 archsimd.Float32x8) archsimd.Float32x4 {
	r0 := reduceXcorrAVX8Lanes(v0)
	r1 := reduceXcorrAVX8Lanes(v1)
	r2 := reduceXcorrAVX8Lanes(v2)
	r3 := reduceXcorrAVX8Lanes(v3)
	p01 := r0.ConcatPermuteScalarsGrouped(0, 0, 4, 4, r1)
	p23 := r2.ConcatPermuteScalarsGrouped(0, 0, 4, 4, r3)
	return p01.ConcatPermuteScalarsGrouped(0, 2, 4, 6, p23).GetLo()
}

func xcorrKernelAVX8SplitForTest(x, y []float32, sum *[8]float32, length int) {
	xcorrKernelAVX4(x, y, (*[4]float32)(sum[:4]), length)
	xcorrKernelAVX4(x, y[4:], (*[4]float32)(sum[4:]), length)
}

func TestXcorrKernelAVX8OnePassBitExact(t *testing.T) {
	if !libopusFloatPitchXCorrUsesAVX2FMA() {
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
			want := xcorrKernelAVX8Scalar(x, y, length)
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

func TestXcorrKernelAVX8OnePassExceptionalParityAndZeroAlloc(t *testing.T) {
	if !libopusFloatPitchXCorrUsesAVX2FMA() {
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

const (
	libopusPitchXcorrOutputMagic   = "GXCO"
	libopusPitchXcorrOracleVersion = uint32(3)
)

var libopusPitchXcorrSIMDHelper libopustest.HelperCache
var libopusPitchXcorrPrimitiveHelper libopustest.HelperCache

type libopusPitchXcorrMetadata struct {
	arch     uint32
	cpu      uint32
	dispatch uint32
	presumed uint32
}

func getLibopusPitchXcorrSIMDHelperPath() (string, error) {
	cflags := []string{"-DHAVE_CONFIG_H"}
	if os.Getenv("GOPUS_REQUIRE_NATIVE_AVX2_FMA") == "1" {
		cflags = append(cflags, "-DGOPUS_REQUIRE_NATIVE_AVX2_FMA=1")
	}
	return libopusPitchXcorrSIMDHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "CELT native SIMD pitch xcorr",
		OutputBase:   "gopus_libopus_celt_pitch_xcorr_simd",
		SourceFile:   "libopus_celt_pitch_xcorr_simd_info.c",
		ProbeRelPath: "config.h",
		CFlags:       cflags,
		RefIncludes:  []string{"celt"},
		SIMDRef:      true,
		Libs:         []string{libopustest.SIMDRefPath(".libs", "libopus.a"), "-lm"},
	})
}

func probeLibopusPitchXcorrSIMD(cases []libopusPitchXcorrCase) (libopusPitchXcorrMetadata, [][]float32, error) {
	payload, err := buildLibopusPitchXcorrInput(cases)
	if err != nil {
		return libopusPitchXcorrMetadata{}, nil, err
	}
	binPath, err := getLibopusPitchXcorrSIMDHelperPath()
	if err != nil {
		return libopusPitchXcorrMetadata{}, nil, err
	}
	data, err := libopustest.RunHelper(binPath, payload)
	if err != nil {
		return libopusPitchXcorrMetadata{}, nil, err
	}
	reader, version, err := libopustest.NewOracleReaderVersion("CELT native SIMD pitch xcorr", libopusPitchXcorrOutputMagic, data)
	if err != nil {
		return libopusPitchXcorrMetadata{}, nil, err
	}
	if version != libopusPitchXcorrOracleVersion {
		return libopusPitchXcorrMetadata{}, nil, fmt.Errorf("CELT native SIMD pitch xcorr helper version=%d want %d", version, libopusPitchXcorrOracleVersion)
	}
	metadata := libopusPitchXcorrMetadata{arch: reader.U32(), cpu: reader.U32(), dispatch: reader.U32(), presumed: reader.U32()}
	count := reader.Count(len(cases))
	want := make([][]float32, count)
	for i := range want {
		length := int(reader.U32())
		if length != cases[i].maxPitch {
			return libopusPitchXcorrMetadata{}, nil, fmt.Errorf("%s C output length=%d want %d", cases[i].name, length, cases[i].maxPitch)
		}
		want[i] = make([]float32, length)
		for j := range want[i] {
			want[i][j] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return libopusPitchXcorrMetadata{}, nil, err
	}
	return metadata, want, nil
}

func TestPitchXCorrPairedLibopusSIMDRawBits(t *testing.T) {
	requireNativeEnv := os.Getenv("GOPUS_REQUIRE_NATIVE_AVX2_FMA") == "1"
	if requireNativeEnv && !libopustest.OracleEnabled() {
		t.Fatal("GOPUS_REQUIRE_NATIVE_AVX2_FMA=1 requires the libopus oracle, but oracle policy disables it")
	}
	libopustest.RequireOracle(t)
	requireNative := requireNativeEnv || libopustest.StrictRefRequired()
	if !libopusFloatPitchXCorrUsesAVX2FMA() {
		if requireNative {
			t.Fatalf("native SIMD CI requires Go AVX2/FMA xcorr dispatch")
		}
		t.Skip("Go AVX2/FMA xcorr dispatch is unavailable on this host")
	}
	cases := libopusPitchXcorrExceptionalCases()
	metadata, want, err := probeLibopusPitchXcorrSIMD(cases)
	if err != nil {
		if requireNativeEnv {
			t.Fatalf("run required paired native libopus xcorr oracle: %v", err)
		}
		libopustest.HelperUnavailable(t, "paired native libopus xcorr oracle", err)
		return
	}
	if !libopustest.NativeX86PitchXCorrMetadataValid(metadata.arch, metadata.cpu, metadata.dispatch, metadata.presumed) {
		t.Fatalf("paired libopus did not select the expected xcorr/tail dispatch: arch=%d cpu=%03b dispatch=%02b presumed=%02b", metadata.arch, metadata.cpu, metadata.dispatch, metadata.presumed)
	}
	stampPath := libopustest.SIMDRefPath(".gopus-libopus-build")
	stamp, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatalf("read paired libopus SIMD build stamp %s: %v", stampPath, err)
	}
	t.Logf("paired libopus SIMD stamp=%s\nselected_arch=%d cpu_features=%03b effective_dispatch=%02b presumed_dispatch=%02b", strings.TrimSpace(string(stamp)), metadata.arch, metadata.cpu, metadata.dispatch, metadata.presumed)
	primitivePath, err := libopusPitchXcorrPrimitiveHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "native xcorr primitive probe",
		OutputBase:   "gopus_libopus_pitch_xcorr_primitives",
		SourceFile:   "libopus_pitch_xcorr_primitive_probe.c",
		ProbeRelPath: "config.h",
		SIMDRef:      true,
	})
	if err != nil {
		t.Fatalf("build native xcorr primitive probe: %v", err)
	}
	primitiveBits, err := libopustest.RunHelper(primitivePath, nil)
	if err != nil {
		t.Fatalf("run native xcorr primitive probe: %v", err)
	}
	t.Logf("native xcorr primitive helper=%s\nbits:\n%s", primitivePath, strings.TrimSpace(string(primitiveBits)))

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]float32, tc.maxPitch)
			pitchXCorrFloat32(tc.x, tc.y, got, len(tc.x), tc.maxPitch)
			t.Run("production", func(t *testing.T) {
				assertPitchXcorrBits(t, got, want[i])
				if allocs := testing.AllocsPerRun(100, func() {
					pitchXCorrFloat32(tc.x, tc.y, got, len(tc.x), tc.maxPitch)
				}); allocs != 0 {
					t.Errorf("warm production pitch xcorr allocated %v times", allocs)
				}
			})

			got = make([]float32, tc.maxPitch)
			pitchXCorrFloat32AVX2FMAOrder(tc.x, tc.y, got, len(tc.x), tc.maxPitch)
			t.Run("direct-dispatch", func(t *testing.T) { assertPitchXcorrBits(t, got, want[i]) })

			got = make([]float32, tc.maxPitch)
			pitchXCorrFloat32AVX2FMAOrderTiny(tc.x, tc.y, got, len(tc.x), tc.maxPitch)
			t.Run("tiny", func(t *testing.T) { assertPitchXcorrBits(t, got, want[i]) })

			if tc.maxPitch < 8 {
				return
			}
			var direct, split, onePass [8]float32
			xcorrKernelAVX8(tc.x, tc.y, &direct, len(tc.x))
			xcorrKernelAVX8SplitForTest(tc.x, tc.y, &split, len(tc.x))
			xcorrKernelAVX8OnePass(tc.x, tc.y, &onePass, len(tc.x))
			t.Run("direct8", func(t *testing.T) { assertPitchXcorrBits(t, direct[:], want[i][:8]) })
			t.Run("split8", func(t *testing.T) { assertPitchXcorrBits(t, split[:], want[i][:8]) })
			t.Run("onepass8", func(t *testing.T) { assertPitchXcorrBits(t, onePass[:], want[i][:8]) })
		})
	}
}

func assertPitchXcorrBits(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("length=%d want %d", len(got), len(want))
		return
	}
	for i := range got {
		gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i])
		if gotBits != wantBits {
			t.Errorf("first difference at correlation %d: got=%08x want=%08x", i, gotBits, wantBits)
			return
		}
	}
}

func libopusPitchXcorrExceptionalCases() []libopusPitchXcorrCase {
	const pitches = 10
	rng := rand.New(rand.NewSource(0x417ec0))
	makeCase := func(name string, length, maxPitch int, fill func(x, y []float32)) libopusPitchXcorrCase {
		x := make([]float32, length)
		y := make([]float32, length+maxPitch-1)
		fill(x, y)
		return libopusPitchXcorrCase{name: name, x: x, y: y, maxPitch: maxPitch}
	}
	cases := []libopusPitchXcorrCase{
		makeCase("length17_random_lanes2and9", 17, pitches, func(x, y []float32) {
			for i := range x {
				x[i] = float32(rng.NormFloat64())
			}
			for i := range y {
				y[i] = float32(rng.NormFloat64())
			}
		}),
		makeCase("length17_correlations10", 17, pitches, func(x, y []float32) {
			for i := range x {
				x[i] = float32(rng.NormFloat64())
			}
			for i := range y {
				y[i] = float32(rng.NormFloat64())
			}
		}),
		makeCase("length10_correlations10", 10, pitches, func(x, y []float32) {
			for i := range x {
				x[i] = float32((i%5)-2) * 0.125
			}
			for i := range y {
				y[i] = float32((i%7)-3) * 0.0625
			}
		}),
		makeCase("length5_existing_tiny_nan_signedzero", 5, 8, func(x, y []float32) {
			copy(x, []float32{0, math.Float32frombits(1 << 31), 1, -1, 0.5})
			y[0] = float32(math.Inf(-1))
			y[1] = 1
			y[4] = math.Float32frombits(0x7fc01234)
		}),
		makeCase("finite_cancellation", 17, pitches, func(x, y []float32) {
			pattern := [...]float32{1e20, 1, -1e20, 1, -1e20, 1, 1e20, 1}
			for i := range x {
				x[i] = pattern[i%len(pattern)]
			}
			for i := range y {
				y[i] = 1
			}
		}),
		makeCase("signed_zero", 17, pitches, func(x, y []float32) {
			for i := range x {
				x[i] = math.Float32frombits(uint32(i&1) << 31)
			}
			for i := range y {
				y[i] = math.Float32frombits(uint32((i+1)&1) << 31)
			}
		}),
		makeCase("subnormals", 17, pitches, func(x, y []float32) {
			for i := range x {
				x[i] = math.Float32frombits(uint32(i%3+1) | uint32(i&1)<<31)
			}
			for i := range y {
				y[i] = math.Float32frombits(uint32(i%5+1) | uint32((i+1)&1)<<31)
			}
		}),
		makeCase("infinities", 17, pitches, func(x, y []float32) {
			for i := range x {
				switch i % 4 {
				case 0:
					x[i] = float32(math.Inf(1))
				case 1:
					x[i] = float32(math.Inf(-1))
				case 2:
					x[i] = 0
				default:
					x[i] = 1
				}
			}
			for i := range y {
				switch i % 3 {
				case 0:
					y[i] = 0
				case 1:
					y[i] = float32(math.Inf(1))
				default:
					y[i] = -1
				}
			}
		}),
		makeCase("distinct_nan_payloads", 17, pitches, func(x, y []float32) {
			for i := range x {
				x[i] = float32(i%7-3) * 0.25
			}
			for i := range y {
				y[i] = float32(i%5-2) * 0.5
			}
			x[3] = math.Float32frombits(0x7fc01234)
			y[8] = math.Float32frombits(0xffc05678)
		}),
		makeCase("length9_fma32_single_round", 9, 8, func(x, y []float32) {
			// Lane zero is seeded by c and then updated by a*b. The exact
			// hardware FMA32 result differs from float32(math.FMA(float64(...))).
			x[0], y[0] = math.Float32frombits(0xa20c2545), 1
			x[8], y[8] = math.Float32frombits(0x3fcca800), math.Float32frombits(0x3f979800)
		}),
		makeCase("length10_sse_nan_operand_priority", 10, pitches, func(x, y []float32) {
			// The last correlation hits -0 * -Inf, then a distinct NaN in
			// its next four-lane SSE update. The C accumulator is the addend.
			x[1], y[10] = math.Float32frombits(0x80000000), float32(math.Inf(-1))
			x[5], y[14] = -0.5, math.Float32frombits(0x7fc01234)
		}),
	}
	for _, length := range []int{9, 10, 15, 17} {
		cases = append(cases, makeCase(fmt.Sprintf("length%d_negative_underflow_tail", length), length, pitches, func(x, y []float32) {
			for i := range x {
				x[i] = math.Float32frombits(0x80000001)
			}
			for i := range y {
				y[i] = math.Float32frombits(0x00000001)
			}
		}))
	}
	for variant, operands := range [][3]uint32{
		{0x7fc01234, 0xffc05678, 0x3f800000},
		{0x7fa01234, 0xffc05678, 0x3f800000},
		{0x7fc01234, 0xffa05678, 0x3f800000},
		{0x7fa01234, 0xffa05678, 0x3f800000},
		{0x7f800000, 0x00000000, 0xffc05678},
		{0x7f800000, 0xff800000, 0x3f800000},
	} {
		operands := operands
		cases = append(cases, makeCase(fmt.Sprintf("nan_fma_priority_%d", variant), 17, pitches, func(x, y []float32) {
			for i := range x {
				x[i] = 1
			}
			for i := range y {
				y[i] = 1
			}
			x[0] = math.Float32frombits(operands[0])
			y[0] = math.Float32frombits(operands[1])
			x[8] = math.Float32frombits(operands[2])
			y[8] = 1
		}))
	}
	cases = append(cases,
		makeCase("avx_hadd_distinct_nan_lanes8", 8, 8, func(x, y []float32) {
			for i := range x {
				bits := uint32(0x7fc00010 + i)
				if i&1 != 0 {
					bits = 0xffa00020 + uint32(i)
				}
				x[i] = math.Float32frombits(bits)
			}
			for i := range y {
				y[i] = 1
			}
		}),
		makeCase("avx_fma231_acc_vs_y_nan17", 17, 8, func(x, y []float32) {
			for i := range x {
				x[i] = 1
			}
			for i := range y {
				y[i] = 1
			}
			x[0] = math.Float32frombits(0x7fc01234)
			y[8] = math.Float32frombits(0xffa05678)
		}),
		makeCase("avx_fma231_acc_vs_x_nan17", 17, 8, func(x, y []float32) {
			for i := range x {
				x[i] = 1
			}
			for i := range y {
				y[i] = 1
			}
			x[0] = math.Float32frombits(0x7fc01234)
			x[8] = math.Float32frombits(0xffa05678)
		}),
		makeCase("sse_mul_distinct_nan4", 4, 1, func(x, y []float32) {
			for i := range x {
				x[i] = 1
				y[i] = 1
			}
			x[0] = math.Float32frombits(0x7fc01234)
			y[0] = math.Float32frombits(0xffa05678)
		}),
		makeCase("sse_high_lane_nan4", 4, 1, func(x, y []float32) {
			for i := range x {
				x[i] = 1
				y[i] = 1
			}
			x[0] = math.Float32frombits(0x7fc01234)
			x[2] = math.Float32frombits(0xffa05678)
		}),
		makeCase("sse_acc_vs_product_nan8", 8, 1, func(x, y []float32) {
			for i := range x {
				x[i] = 1
				y[i] = 1
			}
			x[0] = math.Float32frombits(0x7fc01234)
			x[4] = math.Float32frombits(0xffa05678)
		}),
		makeCase("sse_third_tail_mul_nan7", 7, 1, func(x, y []float32) {
			for i := range x {
				x[i] = 1
				y[i] = 1
			}
			x[6] = math.Float32frombits(0x7fc01234)
			y[6] = math.Float32frombits(0xffa05678)
		}),
	)
	values := []float32{
		0, math.Float32frombits(1 << 31), math.SmallestNonzeroFloat32,
		-math.SmallestNonzeroFloat32, 0.5, -0.5, 1, -1,
		float32(math.Inf(1)), float32(math.Inf(-1)), math.Float32frombits(0x7fc01234),
	}
	for _, length := range []int{17, 31, 240, 241} {
		cases = append(cases, makeCase(fmt.Sprintf("length%d_existing_onepass_exceptional_fill", length), length, pitches, func(x, y []float32) {
			for i := range x {
				x[i] = values[(i*5+1)%len(values)]
			}
			for i := range y {
				y[i] = values[(i*7+3)%len(values)]
			}
		}))
	}
	for _, maxPitch := range []int{8, 9, 10} {
		for variant, input := range [][]float32{values, {
			0, math.Float32frombits(1 << 31), math.SmallestNonzeroFloat32,
			-math.SmallestNonzeroFloat32, 0.5, -0.5, 1, -1,
		}} {
			valueSet := input
			v := variant
			pitches := maxPitch
			cases = append(cases, makeCase(fmt.Sprintf("length10_maxpitch%d_edge%d", maxPitch, variant), 10, pitches, func(x, y []float32) {
				for i := range x {
					x[i] = valueSet[(i*5+v*3)%len(valueSet)]
				}
				for i := range y {
					y[i] = valueSet[(i*7+v*5)%len(valueSet)]
				}
				if v == 0 {
					x[0], y[0] = 0, float32(math.Inf(-1))
					x[1], y[1] = 1, math.Float32frombits(0x7fc01234)
					x[2], y[2] = math.Float32frombits(1<<31), -2
				} else {
					x[0], y[0] = 0, -1
					x[1], y[1] = math.Float32frombits(1<<31), 1
					x[2], y[2] = math.SmallestNonzeroFloat32, 0.5
				}
			}))
		}
	}
	return cases
}
