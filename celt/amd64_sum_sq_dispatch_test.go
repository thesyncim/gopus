//go:build amd64 && gopus_sum_sq_asm

package celt

import "testing"

func TestAMD64SumSqFallbackMatchesGeneric(t *testing.T) {
	withAMD64FeaturesForTest(false, false, false, func() {
		input := []float64{-1.5, 2.25, 0.5, -3, 4.5}
		if got, want := sumOfSquaresF64toF32(input, len(input)), float64(float32(1.5*1.5+2.25*2.25+0.5*0.5+3*3+4.5*4.5)); got != want {
			t.Fatalf("sumOfSquaresF64toF32 fallback mismatch: got %v want %v", got, want)
		}
	})
}
