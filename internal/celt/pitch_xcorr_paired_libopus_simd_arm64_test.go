//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var pairedARMPitchXCorrHelper libopustest.HelperCache

func pairedARMPitchXCorrHelperPath() (string, error) {
	return pairedARMPitchXCorrHelper.CHelperPath(libopustest.CHelperConfig{
		Label:        "paired ARM CELT pitch xcorr",
		OutputBase:   "gopus_libopus_celt_pitch_xcorr_arm",
		SourceFile:   "libopus_celt_pitch_xcorr_arm_info.c",
		CFlags:       []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes:  []string{"celt"},
		SIMDRef:      true,
		Libs:         []string{libopustest.SIMDRefPath(".libs", "libopus.a"), "-lm"},
		ProbeRelPath: "config.h",
	})
}

func pairedARMPitchXCorrCases() []libopusPitchXcorrCase {
	rng := rand.New(rand.NewSource(0x6e454f4e))
	cases := make([]libopusPitchXcorrCase, 0, 20)
	for _, tc := range []struct{ length, maxPitch int }{
		{1, 4}, {2, 5}, {3, 7}, {4, 8}, {5, 9}, {7, 4},
		{8, 5}, {9, 7}, {16, 8}, {17, 9}, {240, 244}, {480, 13},
	} {
		x := make([]float32, tc.length)
		y := make([]float32, tc.length+tc.maxPitch-1)
		for i := range x {
			x[i] = float32(rng.NormFloat64())
		}
		for i := range y {
			y[i] = float32(rng.NormFloat64())
		}
		cases = append(cases, libopusPitchXcorrCase{
			name: fmt.Sprintf("random_len%d_pitch%d", tc.length, tc.maxPitch),
			x:    x, y: y, maxPitch: tc.maxPitch,
		})
	}
	const length, maxPitch = 240, 244
	x := make([]float32, length)
	y := make([]float32, length+maxPitch-1)
	for i := range x {
		sign := float32(1)
		if i&1 != 0 {
			sign = -1
		}
		x[i] = sign + float32(0.0001*math.Sin(float64(i)*0.037))
	}
	for i := range y {
		y[i] = float32(0.9 + 0.001*math.Sin(float64(i)*0.17))
	}
	cases = append(cases, libopusPitchXcorrCase{
		name: "finite_cancellation_len240_pitch244",
		x:    x, y: y, maxPitch: maxPitch,
	})
	for _, tc := range []struct {
		name string
		x0   uint32
		y0   uint32
		x8   uint32
	}{
		{"distinct_qnan_operands", 0x7fc01234, 0xffc05678, 0x3f800000},
		{"snan_x_qnan_y", 0x7fa01234, 0xffc05678, 0x3f800000},
		{"qnan_x_snan_y", 0x7fc01234, 0xffa05678, 0x3f800000},
		{"invalid_inf_zero_then_nan", 0x7f800000, 0x00000000, 0xffc05678},
		{"opposing_infinities", 0x7f800000, 0x3f800000, 0xff800000},
	} {
		x := make([]float32, 17)
		y := make([]float32, 24)
		for i := range x {
			x[i] = 1
		}
		for i := range y {
			y[i] = 1
		}
		x[0], y[0], x[8] = math.Float32frombits(tc.x0), math.Float32frombits(tc.y0), math.Float32frombits(tc.x8)
		cases = append(cases, libopusPitchXcorrCase{name: tc.name, x: x, y: y, maxPitch: 8})
	}
	for _, tc := range []struct {
		name     string
		maxPitch int
		fill     func(x, y []float32)
	}{
		{"late_distinct_qnan", 8, func(x, y []float32) {
			for i := range x {
				x[i] = 1
			}
			for i := range y {
				y[i] = 1
			}
			x[0] = math.Float32frombits(0x7fc01234)
			x[8] = math.Float32frombits(0xffc05678)
		}},
		{"signed_zero_remainder", 5, func(x, y []float32) {
			for i := range x {
				x[i] = math.Float32frombits(uint32(i&1) << 31)
			}
			for i := range y {
				y[i] = math.Float32frombits(uint32((i+1)&1) << 31)
			}
		}},
		{"subnormal_remainder", 7, func(x, y []float32) {
			for i := range x {
				x[i] = math.Float32frombits(uint32(i%3+1) | uint32(i&1)<<31)
			}
			for i := range y {
				if i&1 == 0 {
					y[i] = 1
				} else {
					y[i] = -0.5
				}
			}
		}},
	} {
		x := make([]float32, 17)
		y := make([]float32, 17+tc.maxPitch-1)
		tc.fill(x, y)
		cases = append(cases, libopusPitchXcorrCase{name: tc.name, x: x, y: y, maxPitch: tc.maxPitch})
	}
	// A one-lag call uses celt_inner_prod_neon in the C oracle and the
	// corresponding pitch-tail inner product path in Go. Distinct qNaNs expose
	// the ARM FMLA/FMADD multiplicand order for scalar-only, vector, and tail cases.
	for _, length := range []int{1, 3, 4, 5, 7, 8, 9, 12} {
		x := make([]float32, length)
		y := make([]float32, length)
		for i := range x {
			x[i], y[i] = 1, 1
		}
		x[length-1] = math.Float32frombits(0x7fc01234)
		y[length-1] = math.Float32frombits(0xffc05678)
		cases = append(cases, libopusPitchXcorrCase{
			name:     fmt.Sprintf("one_lag_nan_operands_len%d", length),
			x:        x,
			y:        y,
			maxPitch: 1,
		})
	}
	// These vectors isolate the low/high lane reduction's NaN payload order.
	for _, length := range []int{4, 8, 12} {
		x := make([]float32, length)
		y := make([]float32, length)
		for i := range x {
			x[i], y[i] = 1, 1
		}
		x[0] = math.Float32frombits(0x7fc01234)
		x[2] = math.Float32frombits(0xffc05678)
		cases = append(cases, libopusPitchXcorrCase{
			name:     fmt.Sprintf("one_lag_nan_reduction_len%d", length),
			x:        x,
			y:        y,
			maxPitch: 1,
		})
	}
	return cases
}

func TestPitchXCorrPairedLibopusARMRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	if variant := requirePairedCELTOracleMode(t); variant != "simd" {
		t.Fatalf("paired CELT ARM pitch reference=%s, want SIMD", variant)
	}
	cases := pairedARMPitchXCorrCases()
	payload, err := buildLibopusPitchXcorrInput(cases)
	if err != nil {
		t.Fatal(err)
	}
	path, err := pairedARMPitchXCorrHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "paired ARM CELT pitch xcorr", err)
		return
	}
	data, err := libopustest.RunHelper(path, payload)
	if err != nil {
		libopustest.HelperUnavailable(t, "paired ARM CELT pitch xcorr", err)
		return
	}
	reader, version, err := libopustest.NewOracleReaderVersion("paired ARM CELT pitch xcorr", "GXAO", data)
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("paired ARM CELT pitch version=%d want 1", version)
	}
	if selectedNEON := reader.U32(); selectedNEON != 1 {
		t.Fatalf("paired C ARM xcorr selected NEON=%d want 1", selectedNEON)
	}
	reader.Count(len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reader.U32(); got != uint32(tc.maxPitch) {
				t.Fatalf("C xcorr length=%d want %d", got, tc.maxPitch)
			}
			want := make([]float32, tc.maxPitch)
			for i := range want {
				want[i] = reader.Float32()
			}
			if err := reader.Err(); err != nil {
				t.Fatal(err)
			}
			if tc.maxPitch == 1 {
				inner := celtInnerProd8FMA32(tc.x, tc.y, len(tc.x))
				if gotBits, wantBits := math.Float32bits(inner), math.Float32bits(want[0]); gotBits != wantBits {
					t.Fatalf("inner product=%08x selected C=%08x", gotBits, wantBits)
				}
			}
			got := make([]float32, tc.maxPitch)
			pitchXCorrFloat32Quality(tc.x, tc.y, got, len(tc.x), tc.maxPitch)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("xcorr[%d]=%08x selected C=%08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}
			if allocs := testing.AllocsPerRun(100, func() {
				pitchXCorrFloat32Quality(tc.x, tc.y, got, len(tc.x), tc.maxPitch)
			}); allocs != 0 {
				t.Fatalf("warm pitch xcorr allocated %.2f times per call", allocs)
			}
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestCeltInnerProd8FMA32ARMNoAllocs(t *testing.T) {
	x := make([]float32, 240)
	y := make([]float32, len(x))
	for i := range x {
		x[i] = float32(i%13) * 0.125
		y[i] = float32(i%7) * -0.25
	}
	_ = celtInnerProd8FMA32(x, y, len(x))
	if allocs := testing.AllocsPerRun(100, func() {
		innerProdBenchSink = celtInnerProd8FMA32(x, y, len(x))
	}); allocs != 0 {
		t.Fatalf("inner product allocated %g times per run", allocs)
	}
}
