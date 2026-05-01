package celt

import (
	"reflect"
	"testing"
)

func haar1LegacyGeneric(x []float64, n0, stride int) {
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
			tmp1 := invSqrt2 * float32(x[idx0])
			tmp2 := invSqrt2 * float32(x[idx1])
			x[idx0] = float64(tmp1 + tmp2)
			x[idx1] = float64(tmp1 - tmp2)
			idx0 += step
			idx1 += step
		}
	}
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
			n := tc.n0 * tc.stride
			input := make([]float64, n)
			for i := range input {
				input[i] = float64((i%29)-14) * 0.125
			}
			want := append([]float64(nil), input...)
			got := append([]float64(nil), input...)

			haar1LegacyGeneric(want, tc.n0, tc.stride)
			haar1(got, tc.n0, tc.stride)
			if !reflect.DeepEqual(got, want) {
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
