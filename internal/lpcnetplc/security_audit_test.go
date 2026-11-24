package lpcnetplc

import (
	"math"
	"testing"
)

func TestPeriodFromFeaturesSanitizesOutOfRangeAndNaN(t *testing.T) {
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
		if got < 0 || got > PitchMaxPeriod {
			t.Fatalf("PeriodFromFeatures(%v) = %d, want in [0, %d]", v, got, PitchMaxPeriod)
		}
	}
}
