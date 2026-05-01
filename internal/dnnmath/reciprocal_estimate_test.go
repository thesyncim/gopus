package dnnmath

import (
	"math"
	"testing"
)

func TestReciprocalEstimate32FiniteAndBounded(t *testing.T) {
	for _, x := range []float32{0.125, 0.25, 0.5, 1, 2, 8, 31.5, 128, 1024} {
		got := reciprocalEstimate32(x)
		want := 1 / x
		if math.IsNaN(float64(got)) || math.IsInf(float64(got), 0) {
			t.Fatalf("reciprocalEstimate32(%g) = %g, want finite", x, got)
		}
		rel := math.Abs(float64((got - want) / want))
		if rel > 0.01 {
			t.Fatalf("reciprocalEstimate32(%g) = %g, want about %g, rel=%g", x, got, want, rel)
		}
	}
}

func FuzzReciprocalEstimate32FiniteAndBounded(f *testing.F) {
	for _, bits := range []uint32{0x3e000000, 0x3f000000, 0x3f800000, 0x40000000, 0x41000000, 0x42fc0000} {
		f.Add(bits)
	}
	f.Fuzz(func(t *testing.T, bits uint32) {
		x := math.Float32frombits((bits & 0x7fffff) | 0x3f000000)
		got := reciprocalEstimate32(x)
		want := 1 / x
		rel := math.Abs(float64((got - want) / want))
		if math.IsNaN(float64(got)) || math.IsInf(float64(got), 0) || rel > 0.01 {
			t.Fatalf("reciprocalEstimate32(%g) = %g, want about %g, rel=%g", x, got, want, rel)
		}
	})
}
