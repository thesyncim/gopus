//go:build arm64 && !purego

package celt

import "testing"

func TestSumOfSquaresF64toF32Arm64MatchesLibopusNEONOrder(t *testing.T) {
	x := arm64SumOrderFixture()

	got := float32(sumOfSquaresF64toF32(x, len(x)))
	if got != float32(2650762.5) {
		t.Fatalf("arm64 lane-order sum=%v, want %v", got, float32(2650762.5))
	}
	if got == sequentialSumOfSquaresF64toF32ForTest(x) {
		t.Fatalf("arm64 sum unexpectedly collapsed to sequential accumulation: %v", got)
	}
}

func TestComputeBandRMSUsesArm64LibopusInnerProdOrder(t *testing.T) {
	x := make([]float64, 1001)
	x[0] = 1e4
	for i := 1; i < len(x); i++ {
		x[i] = 1
	}

	laneSum := float32(sumOfSquaresF64toF32(x, len(x)))
	seqSumNoEpsilon := sequentialSumOfSquaresF64toF32ForTest(x)
	if laneSum == seqSumNoEpsilon {
		t.Fatalf("fixture did not expose arm64 lane-order accumulation: %v", laneSum)
	}

	sum := float32(1e-27) + laneSum
	want := 0.5 * float64(celtLog2(sum))

	got := computeBandRMS(x, 0, len(x))
	if got != want {
		t.Fatalf("computeBandRMS=%v, want %v from arm64 lane-order sum", got, want)
	}

	seqSum := float32(1e-27) + sequentialSumOfSquaresF64toF32ForTest(x)
	seq := 0.5 * float64(celtLog2(seqSum))
	if got == seq {
		t.Fatalf("computeBandRMS unexpectedly collapsed to sequential accumulation: %v", got)
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
