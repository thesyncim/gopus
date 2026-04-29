//go:build arm64

package celt

import "testing"

func TestSumOfSquaresF64toF32Arm64MatchesLibopusNEONOrder(t *testing.T) {
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

	got := float32(sumOfSquaresF64toF32(x, len(x)))
	if got != float32(2650762.5) {
		t.Fatalf("arm64 lane-order sum=%v, want %v", got, float32(2650762.5))
	}
	if got == sequentialSumOfSquaresF64toF32ForTest(x) {
		t.Fatalf("arm64 sum unexpectedly collapsed to sequential accumulation: %v", got)
	}
}

func sequentialSumOfSquaresF64toF32ForTest(x []float64) float32 {
	sum := float32(0)
	for _, v := range x {
		v32 := float32(v)
		sum += v32 * v32
	}
	return sum
}
