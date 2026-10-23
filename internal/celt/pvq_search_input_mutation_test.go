package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestPVQSearchInputMutationMatchesScratch(t *testing.T) {
	negativeZero := math.Float32frombits(1 << 31)
	cases := [][]float32{
		{1, -1},
		{0, negativeZero, 0, negativeZero},
		{-0.25, 0.5, -0.75, 0, 0.25, -0.125, 0.625},
		{1e-30, -1e-30, 0, negativeZero, 1e-30},
		{float32(math.Inf(1)), -0.5, float32(math.Inf(-1)), 0.25},
		{math.Float32frombits(0x7fc01234), -0.5, 0.25, -1, 0},
	}
	for caseIndex, source := range cases {
		for _, k := range []int{0, 1, len(source) / 2, 2 * len(source)} {
			t.Run(fmt.Sprintf("case%d-k%d", caseIndex, k), func(t *testing.T) {
				storage := make([]float32, len(source)+2)
				storage[0], storage[len(storage)-1] = -123.25, 456.75
				input := storage[1 : len(storage)-1 : len(storage)-1]
				copy(input, source)
				var wantIY []int32
				var wantSigns []byte
				var wantY, wantAbs []float32
				wantIY, wantYY := opPVQSearchScratchNorm(input, k, &wantIY, &wantSigns, &wantY, &wantAbs)
				for i, want := range source {
					if math.Float32bits(input[i]) != math.Float32bits(want) {
						t.Fatalf("scratch search changes input[%d]", i)
					}
				}

				var gotIY []int32
				var gotSigns []byte
				var gotY, gotAbs []float32
				gotIY, gotYY := opPVQSearchScratchNormWithInputMutation(input, k, &gotIY, &gotSigns, &gotY, &gotAbs, true)
				if !equalInt32Slices(gotIY, wantIY) || math.Float32bits(gotYY) != math.Float32bits(wantYY) {
					t.Fatalf("mutating search pulses=%v yy=%08x; scratch pulses=%v yy=%08x", gotIY, math.Float32bits(gotYY), wantIY, math.Float32bits(wantYY))
				}
				if storage[0] != -123.25 || storage[len(storage)-1] != 456.75 {
					t.Fatal("search changes samples outside its input")
				}
				wantInput := source
				if !useX86PVQSearchSSE2 && k > 0 {
					// The scalar C search leaves the absolute input in X,
					// including its high-pulse invalid-input replacement.
					wantInput = wantAbs
				}
				for i, want := range wantInput {
					if math.Float32bits(input[i]) != math.Float32bits(want) {
						t.Fatalf("input[%d]=%08x, want %08x", i, math.Float32bits(input[i]), math.Float32bits(want))
					}
				}
			})
		}
	}
}
