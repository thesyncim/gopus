package dnnmath

import (
	"math"
	"testing"
)

func TestExpApproxMatchesLibopusScalarBits(t *testing.T) {
	tests := []struct {
		x    float32
		bits uint32
	}{
		{-100, 0x00000000},
		{-40, 0x00000000},
		{-10, 0x383e696d},
		{-2.5, 0x3da81981},
		{-1, 0x3ebc5805},
		{-0.125, 0x3f61ef97},
		{0, 0x3f7ffb19},
		{0.125, 0x3f910d54},
		{1, 0x402df50e},
		{2.5, 0x4142e9e8},
		{8, 0x453a4c66},
	}

	for _, tc := range tests {
		got := math.Float32bits(ExpApprox(tc.x))
		if got != tc.bits {
			t.Fatalf("ExpApprox(%g) bits=0x%08x want 0x%08x", tc.x, got, tc.bits)
		}
	}
}

func TestScalarActivationsMatchLibopusBits(t *testing.T) {
	tests := []struct {
		x           float32
		tanhBits    uint32
		sigmoidBits uint32
	}{
		{-8, 0xbf800000, 0x39ac3800},
		{-2, 0xbf76c7f7, 0x3df41388},
		{-0.5, 0xbeec95fa, 0x3ec14fb3},
		{0, 0x00000000, 0x3f000000},
		{0.5, 0x3eec95fa, 0x3f1f5827},
		{2, 0x3f76c7f7, 0x3f617d8f},
		{8, 0x3f800000, 0x3f7fea79},
	}

	for _, tc := range tests {
		if got := math.Float32bits(TanhScalarApprox(tc.x)); got != tc.tanhBits {
			t.Fatalf("TanhScalarApprox(%g) bits=0x%08x want 0x%08x", tc.x, got, tc.tanhBits)
		}
		if got := math.Float32bits(SigmoidScalarApprox(tc.x)); got != tc.sigmoidBits {
			t.Fatalf("SigmoidScalarApprox(%g) bits=0x%08x want 0x%08x", tc.x, got, tc.sigmoidBits)
		}
	}
}

func TestSoftmaxApproxMatchesLibopusScalarBits(t *testing.T) {
	in := []float32{-1, 0, 1, 2}
	out := make([]float32, len(in))
	SoftmaxApprox(out, in, len(in))
	want := []uint32{0x3d034d82, 0x3db2749c, 0x3e728be7, 0x3f24d99c}

	for i, bits := range want {
		got := math.Float32bits(out[i])
		if got != bits {
			t.Fatalf("SoftmaxApprox[%d] bits=0x%08x want 0x%08x", i, got, bits)
		}
	}
}
