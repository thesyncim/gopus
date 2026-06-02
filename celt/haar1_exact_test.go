package celt

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func haar1ReferenceNorm(x []celtNorm, n0, stride int) {
	n0 >>= 1
	if n0 <= 0 || stride <= 0 {
		return
	}
	const invSqrt2 = float32(0.7071067811865476)
	step := stride * 2
	for i := 0; i < stride; i++ {
		idx0 := i
		idx1 := i + stride
		for j := 0; j < n0; j++ {
			haar1PairNorm(x, idx0, idx1, invSqrt2)
			idx0 += step
			idx1 += step
		}
	}
}

func TestHaar1MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)
	makeInput := func(n int, seed uint32) []float32 {
		x := make([]float32, n)
		for i := range x {
			seed = seed*1664525 + 1013904223
			v := float32(int32(seed>>8)%4000-2000) / 1536
			if i%7 == 0 {
				v *= 1.0 / 1024
			}
			x[i] = v
		}
		return x
	}
	cases := []haar1OracleCase{
		{nameHaarCase(4, 1), makeInput(4, 0x1001), 4, 1},
		{nameHaarCase(8, 2), makeInput(16, 0x1002), 8, 2},
		{nameHaarCase(16, 4), makeInput(64, 0x1004), 16, 4},
		{nameHaarCase(48, 6), makeInput(288, 0x1006), 48, 6},
		{nameHaarCase(120, 8), makeInput(960, 0x1008), 120, 8},
		{nameHaarCase(120, 12), makeInput(1440, 0x1012), 120, 12},
	}
	want, err := probeLibopusHaar1(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]celtNorm, len(tc.x))
			for i := range tc.x {
				got[i] = celtNorm(tc.x[i])
			}
			haar1(got, tc.n0, tc.stride)
			for i := range got {
				got32 := float32(got[i])
				if math.Float32bits(got32) != math.Float32bits(want[ci][i]) {
					t.Fatalf("x[%d]=%08x %.10g want %08x %.10g",
						i, math.Float32bits(got32), got32,
						math.Float32bits(want[ci][i]), want[ci][i])
				}
			}
		})
	}
}

func TestHaar1NormMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	requireBitExactFloat(t)
	cases := []haar1OracleCase{
		{nameHaarCase(8, 1), []float32{0.25, -0.5, 0.75, -1, 0.125, -0.25, 0.5, -0.75}, 8, 1},
		{nameHaarCase(16, 2), makeHaarNormInput(32, 0x5012), 16, 2},
		{nameHaarCase(48, 6), makeHaarNormInput(288, 0x5016), 48, 6},
		{nameHaarCase(120, 12), makeHaarNormInput(1440, 0x5020), 120, 12},
	}
	want, err := probeLibopusHaar1(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt vq", err)
	}
	for ci, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]celtNorm, len(tc.x))
			for i := range tc.x {
				got[i] = celtNorm(tc.x[i])
			}
			haar1Norm(got, tc.n0, tc.stride)
			for i := range got {
				got32 := float32(got[i])
				if math.Float32bits(got32) != math.Float32bits(want[ci][i]) {
					t.Fatalf("x[%d]=%08x %.10g want %08x %.10g",
						i, math.Float32bits(got32), got32,
						math.Float32bits(want[ci][i]), want[ci][i])
				}
			}
		})
	}
}

func makeHaarNormInput(n int, seed uint32) []float32 {
	x := make([]float32, n)
	for i := range x {
		seed = seed*1664525 + 1013904223
		v := float32(int32(seed>>8)%3000-1500) / 2048
		if i%11 == 0 {
			v *= 1.0 / 4096
		}
		x[i] = v
	}
	return x
}

func nameHaarCase(n0, stride int) string {
	return fmt.Sprintf("n0_%d_stride_%d", n0, stride)
}

func TestHaar1SpecializedMatchesGeneric(t *testing.T) {
	testCases := []struct {
		name   string
		n0     int
		stride int
	}{
		{name: "stride1", n0: 64, stride: 1},
		{name: "stride2", n0: 64, stride: 2},
		{name: "stride6", n0: 48, stride: 6},
		{name: "stride8", n0: 120, stride: 8},
		{name: "stride12", n0: 120, stride: 12},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.stride == 1 {
				// stride==1 routes haar1 to the NEON kernel, which uses the same
				// separate-FMUL/FADD/FSUB lane math as libopus's NEON path. That
				// is bit-exact with the scalar reference on the non-fused oracle
				// builds, but the fused arm64 build contracts the reference's
				// a*b+c into FMA, so a byte-for-byte match no longer holds there
				// (it is opus_compare-gated instead).
				requireBitExactFloat(t)
			}
			n := tc.n0 * tc.stride
			input := make([]celtNorm, n)
			for i := range input {
				input[i] = celtNorm(float32((i%29)-14) * 0.125)
			}
			want := append([]celtNorm(nil), input...)
			got := append([]celtNorm(nil), input...)

			haar1ReferenceNorm(want, tc.n0, tc.stride)
			haar1(got, tc.n0, tc.stride)
			if gotText := fmt.Sprint(got); gotText != fmt.Sprint(want) {
				t.Fatalf("haar1 mismatch: got %v want %v", got, want)
			}
		})
	}
}

func TestHaar1StrideFastPathsMatchGenericExact(t *testing.T) {
	testCases := []struct {
		name   string
		n0     int
		stride int
	}{
		{name: "stride1", n0: 64, stride: 1},
		{name: "stride2", n0: 64, stride: 2},
		{name: "stride4", n0: 64, stride: 4},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			n := tc.n0 * tc.stride * 2
			input := make([]float64, n)
			for i := range input {
				switch i % 5 {
				case 0:
					input[i] = float64((i%31)-15) * 0.125
				case 1:
					input[i] = float64((i%29)-14) * -0.0625
				case 2:
					input[i] = float64(i%17) * 1e-6
				case 3:
					input[i] = -float64(i%19) * 0.375
				default:
					input[i] = float64((i%23)-11) * 0.03125
				}
			}

			got := append([]float64(nil), input...)
			want := append([]float64(nil), input...)

			switch tc.stride {
			case 1:
				haar1Stride1Asm(got, tc.n0)
				haar1Stride1Generic(want, tc.n0)
			case 2:
				haar1Stride2Asm(got, tc.n0)
				haar1Stride2Generic(want, tc.n0)
			case 4:
				haar1Stride4Asm(got, tc.n0)
				haar1Stride4(want, tc.n0)
			}

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("exact mismatch: got %v want %v", got, want)
			}
		})
	}
}
