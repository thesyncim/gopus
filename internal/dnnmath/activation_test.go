package dnnmath

import (
	"math"
	"runtime"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
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

func TestSoftmaxApproxMatchesLibopusActivationHackBits(t *testing.T) {
	in := []float32{-1, 0, 1, 2}
	out := make([]float32, len(in))
	SoftmaxApprox(out, in, len(in))

	for i, want := range in {
		if gotBits, wantBits := math.Float32bits(out[i]), math.Float32bits(want); gotBits != wantBits {
			t.Fatalf("SoftmaxApprox[%d] bits=0x%08x want identity 0x%08x", i, gotBits, wantBits)
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

func TestScalarVectorActivationsMatchScalarReferencePath(t *testing.T) {
	in := []float32{-0.75}
	tanhOut := make([]float32, 1)
	TanhVectorScalarApprox(tanhOut, in, 1)
	sigmoidOut := make([]float32, 1)
	SigmoidVectorScalarApprox(sigmoidOut, in, 1)

	if got, want := math.Float32bits(tanhOut[0]), math.Float32bits(TanhScalarApprox(in[0])); got != want {
		t.Fatalf("TanhVectorScalarApprox bits=0x%08x want 0x%08x", got, want)
	}
	if got, want := math.Float32bits(sigmoidOut[0]), math.Float32bits(SigmoidScalarApprox(in[0])); got != want {
		t.Fatalf("SigmoidVectorScalarApprox bits=0x%08x want 0x%08x", got, want)
	}
}

func TestExpVectorApproxMatchesActiveTailPath(t *testing.T) {
	in := []float32{-1, 0, 1, 2, 0.125}
	out := make([]float32, len(in))
	ExpVectorApprox(out, in, len(in))

	want := ExpApprox(in[len(in)-1])
	if runtime.GOARCH == "arm64" {
		want = expApproxNEON(in[len(in)-1])
	}
	if gotBits, wantBits := math.Float32bits(out[len(out)-1]), math.Float32bits(want); gotBits != wantBits {
		t.Fatalf("ExpVectorApprox tail bits=0x%08x want 0x%08x", gotBits, wantBits)
	}
}

func TestExpVectorScalarApproxMatchesScalarReferencePath(t *testing.T) {
	in := []float32{-1, 0, 1, 2, 0.125}
	out := make([]float32, len(in))
	ExpVectorScalarApprox(out, in, len(in))

	for i, x := range in {
		if gotBits, wantBits := math.Float32bits(out[i]), math.Float32bits(ExpApprox(x)); gotBits != wantBits {
			t.Fatalf("ExpVectorScalarApprox[%d] bits=0x%08x want 0x%08x", i, gotBits, wantBits)
		}
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
			want = int8(int(math.Floor(0.5 + float64(float32(127)*x))))
		}
		if got := Cgemv8x4QuantizeInput(x); got != want {
			t.Fatalf("Cgemv8x4QuantizeInput(%g)=%d want %d", x, got, want)
		}
	}
}

func TestCgemv8x4QuantizeInputScalarMatchesScalarReferencePath(t *testing.T) {
	cases := []float32{
		-1,
		float32(-2.5 / 127),
		0,
		float32(2.5 / 127),
		math.Float32frombits(0x3e83060c),
		1,
	}

	for _, x := range cases {
		want := int8(int(math.Floor(0.5 + float64(float32(127)*x))))
		if got := Cgemv8x4QuantizeInputScalar(x); got != want {
			t.Fatalf("Cgemv8x4QuantizeInputScalar(%g)=%d want %d", x, got, want)
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

func TestCeltMathMatchesLibopusCELTOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	logInputs := []float32{
		math.SmallestNonzeroFloat32,
		1e-30, 1e-20, 1e-10, 1e-6, 1.52587890625e-05,
		0.03125, 0.125, 0.5, 0.75, 0.99999994, 1,
		1.0000001, 1.25, 1.5, 1.875, 2, 3.5, 8, 1024,
	}
	for exp := int32(-12); exp <= 12; exp++ {
		for mant := uint32(0); mant < 8; mant++ {
			bits := uint32(exp+127)<<23 | mant<<20 | 0x54321
			logInputs = append(logInputs, math.Float32frombits(bits))
		}
	}
	wantLog, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeLog, logInputs)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, x := range logInputs {
		got := CeltLog(x)
		if math.Float32bits(got) != math.Float32bits(wantLog[i]) {
			t.Fatalf("CeltLog(%g)=%08x(%g) want %08x(%g)",
				x,
				math.Float32bits(got), got,
				math.Float32bits(wantLog[i]), wantLog[i],
			)
		}
	}

	sinInputs := []float32{
		-10, -3.5, -1.25, -1, -0.5, -0.001, 0, 0.001,
		0.125, 0.25, 0.5, 0.75, 0.99999994, 1, 1.0000001,
		1.25, 2, 3, 3.75, 10,
	}
	seed := uint32(0x6d2b79f5)
	for i := 0; i < 128; i++ {
		seed = 1664525*seed + 1013904223
		sinInputs = append(sinInputs, float32(int32(seed%20001)-10000)/8192)
	}
	wantSin, err := libopustest.ProbeCELTMath(libopustest.CELTMathModeSin, sinInputs)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt math", err)
	}
	for i, x := range sinInputs {
		got := CeltSin(x)
		if math.Float32bits(got) != math.Float32bits(wantSin[i]) {
			t.Fatalf("CeltSin(%g)=%08x(%g) want %08x(%g)",
				x,
				math.Float32bits(got), got,
				math.Float32bits(wantSin[i]), wantSin[i],
			)
		}
	}
}
