package lpcnetplc

import (
	"math"
	"testing"
)

func TestPeriodFromFeaturesClampsOutOfRangeAndNaN(t *testing.T) {
	cases := []float32{
		float32(math.NaN()),
		float32(math.Inf(1)),
		float32(math.Inf(-1)),
		100.0,
		-200.0,
		7.6,
	}
	features := make([]float32, NumFeatures)
	for _, v := range cases {
		features[NumBands] = v
		got := PeriodFromFeatures(features)
		if got < PitchMinPeriod || got > PitchMaxPeriod {
			t.Fatalf("PeriodFromFeatures(%v) = %d, want in [%d, %d]", v, got, PitchMinPeriod, PitchMaxPeriod)
		}
	}
}
