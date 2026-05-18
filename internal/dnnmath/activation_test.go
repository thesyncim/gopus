package dnnmath

import (
	"math"
	"runtime"
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

func TestSoftmaxApproxMatchesLibopusActiveBits(t *testing.T) {
	in := []float32{-1, 0, 1, 2}
	out := make([]float32, len(in))
	SoftmaxApprox(out, in, len(in))
	want := []uint32{0x3d034d82, 0x3db2749c, 0x3e728be7, 0x3f24d99c}
	if runtime.GOARCH == "arm64" {
		want = []uint32{0x3d034d84, 0x3db274a4, 0x3e728ba4, 0x3f24d9ab}
	}

	for i, bits := range want {
		got := math.Float32bits(out[i])
		if got != bits {
			t.Fatalf("SoftmaxApprox[%d] bits=0x%08x want 0x%08x", i, got, bits)
		}
	}
}

func TestVectorActivationsMatchActiveTailPath(t *testing.T) {
	in := []float32{-0.75}
	tanhOut := make([]float32, 1)
	TanhVectorApprox(tanhOut, in, 1)
	sigmoidOut := make([]float32, 1)
	SigmoidVectorApprox(sigmoidOut, in, 1)

	wantTanh := TanhScalarApprox(in[0])
	wantSigmoid := SigmoidScalarApprox(in[0])
	if runtime.GOARCH == "arm64" {
		wantTanh = tanhTailNEON(in[0])
		wantSigmoid = sigmoidTailNEON(in[0])
	}
	if got, want := math.Float32bits(tanhOut[0]), math.Float32bits(wantTanh); got != want {
		t.Fatalf("TanhVectorApprox tail bits=0x%08x want 0x%08x", got, want)
	}
	if got, want := math.Float32bits(sigmoidOut[0]), math.Float32bits(wantSigmoid); got != want {
		t.Fatalf("SigmoidVectorApprox tail bits=0x%08x want 0x%08x", got, want)
	}
}

func TestCgemv8x4QuantizeInputMatchesActiveArch(t *testing.T) {
	cases := []float32{
		-1,
		float32(-2.5 / 127),
		0,
		float32(2.5 / 127),
		1,
	}

	for _, x := range cases {
		var want int8
		if runtime.GOARCH == "arm64" {
			want = int8(int32(math.RoundToEven(float64(float32(127) * x))))
		} else {
			want = int8(int(math.Floor(float64(float32(0.5) + float32(127)*x))))
		}
		if got := Cgemv8x4QuantizeInput(x); got != want {
			t.Fatalf("Cgemv8x4QuantizeInput(%g)=%d want %d", x, got, want)
		}
	}
}

func TestCeltMathMatchesLibopusFloatBits(t *testing.T) {
	logTests := []struct {
		x    float32
		bits uint32
	}{
		{1e-6, 0xc15d0c55},
		{1.52587890625e-05, 0xc1317218},
		{0.125, 0xc0051592},
		{1, 0x32317218},
		{3.5, 0x3fa05a8a},
	}
	for _, tc := range logTests {
		if got := math.Float32bits(CeltLog(tc.x)); got != tc.bits {
			t.Fatalf("CeltLog(%g) bits=0x%08x want 0x%08x", tc.x, got, tc.bits)
		}
	}

	sinTests := []struct {
		x    float32
		bits uint32
	}{
		{-10, 0x3ee2b7dc},
		{-1, 0xbf1fcfe5},
		{0, 0x00000000},
		{0.5, 0x3f719795},
		{1, 0x3f1fcfe4},
		{3, 0x3f6650eb},
	}
	for _, tc := range sinTests {
		if got := math.Float32bits(CeltSin(tc.x)); got != tc.bits {
			t.Fatalf("CeltSin(%g) bits=0x%08x want 0x%08x", tc.x, got, tc.bits)
		}
	}
}
