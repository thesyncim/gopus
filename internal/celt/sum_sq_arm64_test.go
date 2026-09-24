//go:build arm64

package celt

import "testing"

func TestSumOfSquaresF64toF32MatchesConfiguredArm64LibopusOrder(t *testing.T) {
	x := arm64SumOrderFixture()

	got := float32(sumOfSquaresF64toF32(x, len(x)))
	if celtFusedFloat {
		if got != float32(2650762.5) {
			t.Fatalf("arm64 SIMD lane-order sum=%v, want %v", got, float32(2650762.5))
		}
		if got == sequentialSumOfSquaresF64toF32ForTest(x) {
			t.Fatalf("arm64 SIMD sum unexpectedly collapsed to sequential accumulation: %v", got)
		}
		return
	}
	if want := sequentialSumOfSquaresF64toF32ForTest(x); got != want {
		t.Fatalf("arm64 scalar-order sum=%v, want %v", got, want)
	}
}

func TestComputeBandRMSUsesArm64LibopusInnerProdOrder(t *testing.T) {
	x := make([]float32, 1001)
	x[0] = 1e4
	for i := 1; i < len(x); i++ {
		x[i] = 1
	}

	got := computeBandRMS(x, 0, len(x))
	sum := float32(1e-27) + celtInnerProdF32LibopusOrder(x)
	want := celtLog2(celtSqrt(sum))
	if got != want {
		t.Fatalf("computeBandRMS=%v, want %v from configured libopus inner product and sqrt-then-log order", got, want)
	}
}

func TestInnerProductNormUsesConfiguredArm64LibopusOrder(t *testing.T) {
	src := arm64SumOrderFixture()
	x := make([]celtNorm, len(src))
	for i, v := range src {
		x[i] = celtNorm(float32(v))
	}

	got := innerProductNorm(x, x)
	want := float32(2650762.5)
	if !celtFusedFloat {
		want = sequentialSumOfSquaresF64toF32ForTest(src)
	}
	if got != want {
		t.Fatalf("innerProductNorm=%v, want %v from configured arm64 libopus order", got, want)
	}

	seq := sequentialSumOfSquaresF64toF32ForTest(src)
	if celtFusedFloat && got == seq {
		t.Fatalf("arm64 SIMD innerProductNorm unexpectedly collapsed to sequential accumulation: %v", got)
	}
}

func arm64SumOrderFixture() []float64 {
	x := make([]float64, 21)
	z := uint32(3)
	for i := range x {
		z = 1664525*z + 1013904223
		mag := 1e-2
		if z&1 != 0 {
			mag = 1e3
		}
		x[i] = ((float64(z)/float64(^uint32(0)))*2 - 1) * mag
	}
	return x
}

func sequentialSumOfSquaresF64toF32ForTest(x []float64) float32 {
	sum := float32(0)
	for _, v := range x {
		v32 := float32(v)
		sum += v32 * v32
	}
	return sum
}
