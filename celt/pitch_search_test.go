package celt

import (
	"math/rand"
	"testing"
)

func TestFindBestPitchInRangesMatchesFullSweep(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iter := 0; iter < 500; iter++ {
		length := 16 + rng.Intn(128)
		maxPitch := 8 + rng.Intn(96)
		y := make([]float64, length+maxPitch)
		for i := range y {
			y[i] = rng.Float64()*2 - 1
		}

		a := rng.Intn(maxPitch)
		b := rng.Intn(maxPitch)
		ranges := normalizePitchSearchRanges(
			pitchSearchRange{lo: max(0, a-2), hi: min(maxPitch-1, a+2)},
			pitchSearchRange{lo: max(0, b-2), hi: min(maxPitch-1, b+2)},
		)

		xcorr := make([]float64, maxPitch)
		for _, r := range ranges {
			if r.hi < r.lo {
				continue
			}
			for i := r.lo; i <= r.hi; i++ {
				switch rng.Intn(4) {
				case 0:
					xcorr[i] = 0
				case 1:
					xcorr[i] = -(rng.Float64() * 2)
				default:
					xcorr[i] = rng.Float64() * 8
				}
			}
		}

		var want [2]int
		findBestPitch(xcorr, y, length, maxPitch, &want)

		var got [2]int
		findBestPitchInRanges(xcorr, y, length, ranges, &got)

		if got != want {
			t.Fatalf("iter %d mismatch: got=%v want=%v ranges=%v", iter, got, want, ranges)
		}
	}
}

func TestPitchSearchMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(7))

	for iter := 0; iter < 200; iter++ {
		length := 480
		maxPitch := combFilterMaxPeriod - 3*combFilterMinPeriod
		xLP := make([]float64, length)
		y := make([]float64, length+maxPitch)
		for i := range xLP {
			xLP[i] = rng.Float64()*2 - 1
		}
		for i := range y {
			y[i] = rng.Float64()*2 - 1
		}

		var scratchCurrent, scratchLegacy encoderScratch
		got := pitchSearch(xLP, y, length, maxPitch, &scratchCurrent)
		want := pitchSearchLegacy(xLP, y, length, maxPitch, &scratchLegacy)
		if got != want {
			t.Fatalf("iter %d mismatch: got=%d want=%d", iter, got, want)
		}
	}
}
