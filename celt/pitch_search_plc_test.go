package celt

import (
	"math/rand"
	"testing"
)

func pitchSearchPLCLegacy(xLP []float64, y []float64, length, maxPitch int, scratch *encoderScratch) int {
	if length <= 0 || maxPitch <= 0 {
		return 0
	}
	lag := length + maxPitch

	xLP4 := ensureFloat64Slice(&scratch.prefilterXLP4, length>>2)
	yLP4 := ensureFloat64Slice(&scratch.prefilterYLP4, lag>>2)
	xcorr := ensureFloat64Slice(&scratch.prefilterXcorr, maxPitch>>1)

	for j := 0; j < length>>2; j++ {
		xLP4[j] = xLP[2*j]
	}
	for j := 0; j < lag>>2; j++ {
		yLP4[j] = y[2*j]
	}

	pitchXCorrFloat32(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)
	bestPitch := [2]int{0, 0}
	findBestPitch(xcorr, yLP4, length>>2, maxPitch>>2, &bestPitch)

	halfPitch := maxPitch >> 1
	ranges := pitchSearchFineRanges(bestPitch, halfPitch)
	for _, r := range ranges {
		if r.hi < r.lo {
			continue
		}
		for i := r.lo; i <= r.hi; i++ {
			sum := innerProdFloat32(xLP, y[i:], length>>1)
			if sum < -1 {
				sum = -1
			}
			xcorr[i] = sum
		}
	}
	findBestPitchInRanges(xcorr, y, length>>1, ranges, &bestPitch)

	offset := 0
	if bestPitch[0] > 0 && bestPitch[0] < halfPitch-1 {
		a := xcorr[bestPitch[0]-1]
		b := xcorr[bestPitch[0]]
		c := xcorr[bestPitch[0]+1]
		if (c - a) > 0.7*(b-a) {
			offset = 1
		} else if (a - c) > 0.7*(b-c) {
			offset = -1
		}
	}
	return 2*bestPitch[0] - offset
}

func TestPitchSearchPLCMatchesLegacy(t *testing.T) {
	rng := rand.New(rand.NewSource(71))
	const maxPitch = 720 - 100

	for iter := 0; iter < 200; iter++ {
		length := 480
		xLP := make([]float64, length)
		y := make([]float64, length+maxPitch)
		for i := range xLP {
			xLP[i] = rng.Float64()*2 - 1
		}
		for i := range y {
			y[i] = rng.Float64()*2 - 1
		}

		var scratchCurrent, scratchLegacy encoderScratch
		got := pitchSearchPLC(xLP, y, length, maxPitch, &scratchCurrent)
		want := pitchSearchPLCLegacy(xLP, y, length, maxPitch, &scratchLegacy)
		if got != want {
			t.Fatalf("iter %d mismatch: got=%d want=%d", iter, got, want)
		}
	}
}
