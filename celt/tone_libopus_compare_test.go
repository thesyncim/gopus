//go:build cgo_libopus
// +build cgo_libopus

package celt

import (
	"math"
	"math/rand"
	"testing"
)

func TestToneDetectParityAgainstLibopus(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
		n          = 1080
		iters      = 400
	)

	rng := rand.New(rand.NewSource(20260207))
	pcm := make([]float64, n*channels)
	scratch := make([]float32, n)

	var (
		freqMismatch int
		toneMismatch int
		maxFreqDiff  float64
		maxToneDiff  float64
	)

	for iter := 0; iter < iters; iter++ {
		for i := range pcm {
			pcm[i] = (rng.Float64()*2.0 - 1.0) * CELTSigScale
		}

		goFreq, goTone := toneDetectScratch(pcm, channels, sampleRate, scratch)
		libFreq, libTone := libopusToneDetectRef(pcm, channels, sampleRate)

		fd := math.Abs(goFreq - libFreq)
		td := math.Abs(goTone - libTone)

		// Tone frequency is -1 when no tone detected.
		if (goFreq < 0) != (libFreq < 0) || fd > 1e-5 {
			freqMismatch++
			if fd > maxFreqDiff {
				maxFreqDiff = fd
			}
		}
		if td > 1e-5 {
			toneMismatch++
			if td > maxToneDiff {
				maxToneDiff = td
			}
		}
	}

	t.Logf("iters=%d freqMismatch=%d toneMismatch=%d maxFreqDiff=%.8f maxToneDiff=%.8f",
		iters, freqMismatch, toneMismatch, maxFreqDiff, maxToneDiff)
	if freqMismatch != 0 || toneMismatch != 0 {
		t.Fatalf("tone_detect parity mismatch: freq=%d tone=%d maxFreqDiff=%.8f maxToneDiff=%.8f",
			freqMismatch, toneMismatch, maxFreqDiff, maxToneDiff)
	}
}
