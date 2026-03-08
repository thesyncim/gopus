//go:build amd64 && gopus_spread_asm

package celt

import "testing"

func TestAMD64SpreadCountFallbackMatchesGeneric(t *testing.T) {
	withAMD64FeaturesForTest(false, false, false, func() {
		input := []float64{0.1, 0.4, 0.9, 1.25, -0.05, -0.3}
		got0, got1, got2 := spreadCountThresholds(input, len(input), 0.5)

		var want0, want1, want2 int
		for _, v := range input {
			x2N := v * v * 0.5
			if x2N < 0.25 {
				want0++
			}
			if x2N < 0.0625 {
				want1++
			}
			if x2N < 0.015625 {
				want2++
			}
		}
		if got0 != want0 || got1 != want1 || got2 != want2 {
			t.Fatalf("spreadCountThresholds fallback mismatch: got (%v,%v,%v) want (%v,%v,%v)", got0, got1, got2, want0, want1, want2)
		}
	})
}
