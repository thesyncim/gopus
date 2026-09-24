//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	libopusPitchXcorrOutputMagic   = "GXCO"
	libopusPitchXcorrOracleVersion = uint32(2)

	libopusPitchXcorrCPUAVX2      = uint32(2)
	libopusPitchXcorrCPUFMA       = uint32(4)
	libopusPitchXcorrDispatchAVX2 = uint32(1)
	libopusPitchXcorrDispatchSSE  = uint32(2)
)

var libopusPitchXcorrSIMDHelper libopustest.HelperCache

type libopusPitchXcorrMetadata struct {
	arch     uint32
	cpu      uint32
	dispatch uint32
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
	metadata := libopusPitchXcorrMetadata{arch: reader.U32(), cpu: reader.U32(), dispatch: reader.U32()}
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
	if metadata.arch < 4 || metadata.cpu&(libopusPitchXcorrCPUAVX2|libopusPitchXcorrCPUFMA) != (libopusPitchXcorrCPUAVX2|libopusPitchXcorrCPUFMA) ||
		metadata.dispatch&(libopusPitchXcorrDispatchAVX2|libopusPitchXcorrDispatchSSE) != (libopusPitchXcorrDispatchAVX2|libopusPitchXcorrDispatchSSE) {
		t.Fatalf("paired libopus did not select the expected xcorr/tail dispatch: arch=%d cpu=%03b dispatch=%02b", metadata.arch, metadata.cpu, metadata.dispatch)
	}
	stampPath := libopustest.SIMDRefPath(".gopus-libopus-build")
	stamp, err := os.ReadFile(stampPath)
	if err != nil {
		t.Fatalf("read paired libopus SIMD build stamp %s: %v", stampPath, err)
	}
	t.Logf("paired libopus SIMD stamp=%s\nselected_arch=%d cpu_features=%03b effective_dispatch=%02b", strings.TrimSpace(string(stamp)), metadata.arch, metadata.cpu, metadata.dispatch)

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]float32, tc.maxPitch)
			pitchXCorrFloat32(tc.x, tc.y, got, len(tc.x), tc.maxPitch)
			t.Run("production", func(t *testing.T) { assertPitchXcorrBits(t, got, want[i]) })

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
			xcorrKernelAVX8(&tc.x[0], &tc.y[0], &direct, len(tc.x))
			xcorrKernelAVX8SplitForTest(&tc.x[0], &tc.y[0], &split, len(tc.x))
			xcorrKernelAVX8OnePass(&tc.x[0], &tc.y[0], &onePass, len(tc.x))
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
	}
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
