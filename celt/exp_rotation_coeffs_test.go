package celt

import (
	"math"
	"testing"
)

func TestExpRotationCoefficientsMatchDirectComputation(t *testing.T) {
	for spread := spreadLight; spread <= spreadAggressive; spread++ {
		spreadFactor := expRotationSpreadFactors[spread-1]
		for length := 1; length <= maxExpRotationLength; length++ {
			maxK := (length - 1) >> 1
			if maxK > MaxPVQK {
				maxK = MaxPVQK
			}
			for k := 0; k <= maxK; k++ {
				gotC, gotS, ok := expRotationCoefficients(length, k, spread)
				if !ok {
					t.Fatalf("missing coefficient for length=%d k=%d spread=%d", length, k, spread)
				}
				gain := float32(length) / float32(length+spreadFactor*k)
				theta := 0.5 * gain * gain
				wantC := float64(float32(math.Cos(0.5 * math.Pi * float64(theta))))
				wantS := float64(float32(math.Sin(0.5 * math.Pi * float64(theta))))
				if gotC != wantC || gotS != wantS {
					t.Fatalf("length=%d k=%d spread=%d got=(%0.17g,%0.17g) want=(%0.17g,%0.17g)", length, k, spread, gotC, gotS, wantC, wantS)
				}
			}
		}
	}
}
