//go:build cgo_libopus
// +build cgo_libopus

package celt

import (
	"math"
	"math/rand"
	"testing"
)

func TestPrefilterPitchCoreParityAgainstLibopus(t *testing.T) {
	const (
		channels  = 2
		frameSize = 960
		maxPeriod = combFilterMaxPeriod
		minPeriod = combFilterMinPeriod
		iters     = 500
	)

	rng := rand.New(rand.NewSource(20260207))
	perChanLen := maxPeriod + frameSize
	pre := make([]float64, perChanLen*channels)

	pitchBufLen := (maxPeriod + frameSize) >> 1
	pitchBuf := make([]float64, pitchBufLen)
	scratch := &encoderScratch{}

	var (
		pitchMismatch int
		gainMismatch  int
		maxPitchDiff  int
		maxGainDiff   float64
	)

	for iter := 0; iter < iters; iter++ {
		for i := range pre {
			pre[i] = (rng.Float64()*2.0 - 1.0) * CELTSigScale
		}

		prevPeriod := rng.Intn(maxPeriod)
		prevGain := rng.Float64() * 0.8

		// Go path used by runPrefilter.
		pitchDownsample(pre, pitchBuf, pitchBufLen, channels, 2)
		maxPitch := maxPeriod - 3*minPeriod
		goPitch := pitchSearch(pitchBuf[maxPeriod>>1:], pitchBuf, frameSize, maxPitch, scratch)
		goPitch = maxPeriod - goPitch
		goGain := removeDoubling(pitchBuf, maxPeriod, minPeriod, frameSize, &goPitch, prevPeriod, prevGain, scratch)
		if goPitch > maxPeriod-2 {
			goPitch = maxPeriod - 2
		}
		goGain *= 0.7

		libPitch, libGain := libopusPrefilterPitchCore(pre, channels, frameSize, maxPeriod, minPeriod, prevPeriod, prevGain)

		pd := int(math.Abs(float64(goPitch - libPitch)))
		gd := math.Abs(goGain - libGain)

		if pd != 0 {
			pitchMismatch++
			if pd > maxPitchDiff {
				maxPitchDiff = pd
			}
		}
		if gd > 1e-4 {
			gainMismatch++
			if gd > maxGainDiff {
				maxGainDiff = gd
			}
		}
	}

	t.Logf("iters=%d pitchMismatch=%d gainMismatch=%d maxPitchDiff=%d maxGainDiff=%.6f",
		iters, pitchMismatch, gainMismatch, maxPitchDiff, maxGainDiff)
}
