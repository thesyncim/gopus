package celt

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/util"
)

var pitchSearchSink int

func pitchSearchLegacy(xLP []float64, y []float64, length, maxPitch int, scratch *encoderScratch) int {
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

	prefilterPitchXcorr(xLP4, yLP4, xcorr, length>>2, maxPitch>>2)
	bestPitch := [2]int{0, 0}
	findBestPitch(xcorr, yLP4, length>>2, maxPitch>>2, &bestPitch)

	for i := 0; i < maxPitch>>1; i++ {
		xcorr[i] = 0
		if util.Abs(i-2*bestPitch[0]) > 2 && util.Abs(i-2*bestPitch[1]) > 2 {
			continue
		}
		sum := prefilterInnerProd(xLP, y[i:], length>>1)
		if sum < -1 {
			sum = -1
		}
		xcorr[i] = sum
	}
	findBestPitch(xcorr, y, length>>1, maxPitch>>1, &bestPitch)

	offset := 0
	if bestPitch[0] > 0 && bestPitch[0] < (maxPitch>>1)-1 {
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

func benchmarkPitchSearch(b *testing.B, fn func([]float64, []float64, int, int, *encoderScratch) int) {
	rng := rand.New(rand.NewSource(42))
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
	var scratch encoderScratch
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pitchSearchSink = fn(xLP, y, length, maxPitch, &scratch)
	}
}

func BenchmarkPitchSearchCurrent(b *testing.B) {
	benchmarkPitchSearch(b, pitchSearch)
}

func BenchmarkPitchSearchLegacy(b *testing.B) {
	benchmarkPitchSearch(b, pitchSearchLegacy)
}
