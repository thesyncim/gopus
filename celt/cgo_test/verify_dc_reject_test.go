//go:build cgo_libopus
// +build cgo_libopus

// Package cgo verifies whether libopus applies DC rejection for float CELT mode
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestVerifyLibopusDCReject checks if removing DC rejection from gopus makes it match libopus
func TestVerifyLibopusDCReject(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	mode := celt.GetModeConfig(frameSize)
	shortBlocks := mode.ShortBlocks

	// === GOPUS WITHOUT DC REJECTION (Direct pre-emphasis) ===
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)

	// Skip DC rejection - go directly to pre-emphasis
	preemphNoDC := goEnc.ApplyPreemphasisWithScaling(pcm64)
	mdctNoDC := celt.ComputeMDCTWithHistory(preemphNoDC, make([]float64, 120), shortBlocks)

	// === LIBOPUS PRE-EMPHASIS (for comparison) ===
	libPreemph := ApplyLibopusPreemphasis(pcm32, 0.85)
	libPreemphF64 := make([]float64, len(libPreemph))
	for i, v := range libPreemph {
		libPreemphF64[i] = float64(v)
	}
	mdctLib := celt.ComputeMDCTWithHistory(libPreemphF64, make([]float64, 120), shortBlocks)

	t.Log("=== Band 0 MDCT: gopus (no DC reject) vs libopus ===")
	M := 1 << mode.LM
	maxDiff := 0.0
	for i := 0; i < M; i++ {
		diff := mdctNoDC[i] - mdctLib[i]
		if math.Abs(diff) > math.Abs(maxDiff) {
			maxDiff = diff
		}
		t.Logf("Coeff %d: gopusNoDC=%+.4f libopus=%+.4f diff=%+.6f", i, mdctNoDC[i], mdctLib[i], diff)
	}
	t.Logf("Max diff: %+.6f", maxDiff)

	// Now check if the issue is DELAY BUFFER
	// libopus uses delay_compensation=192, meaning it prepends 192 zeros to the input
	// and drops the last 192 samples of the first frame
	t.Log("")
	t.Log("=== Testing with delay buffer (like libopus) ===")

	// Create input with delay: [zeros] + [pcm] but only take first frameSize
	delayedInput := make([]float64, frameSize)
	delayComp := 192
	// For first frame, first delayComp samples are zeros
	for i := delayComp; i < frameSize; i++ {
		delayedInput[i] = pcm64[i-delayComp]
	}

	goEnc2 := celt.NewEncoder(1)
	goEnc2.Reset()
	preemphDelayed := goEnc2.ApplyPreemphasisWithScaling(delayedInput)
	mdctDelayed := celt.ComputeMDCTWithHistory(preemphDelayed, make([]float64, 120), shortBlocks)

	t.Log("Coeff | gopusNoDC  | gopusDelayed | libopus    | diff(noDC-lib) | diff(delayed-lib)")
	for i := 0; i < M; i++ {
		diffNoDC := mdctNoDC[i] - mdctLib[i]
		diffDelayed := mdctDelayed[i] - mdctLib[i]
		t.Logf("%5d | %+10.4f | %+12.4f | %+10.4f | %+14.6f | %+17.6f",
			i, mdctNoDC[i], mdctDelayed[i], mdctLib[i], diffNoDC, diffDelayed)
	}

	// Check which is closer to libopus
	sumDiffNoDC := 0.0
	sumDiffDelayed := 0.0
	for i := 0; i < M; i++ {
		sumDiffNoDC += math.Abs(mdctNoDC[i] - mdctLib[i])
		sumDiffDelayed += math.Abs(mdctDelayed[i] - mdctLib[i])
	}
	t.Logf("\nTotal abs diff (noDC): %.4f", sumDiffNoDC)
	t.Logf("Total abs diff (delayed): %.4f", sumDiffDelayed)

	if sumDiffNoDC < sumDiffDelayed {
		t.Log("\n==> gopus WITHOUT DC rejection is closer to libopus!")
		t.Log("    The issue is the DC rejection filter, not the delay buffer.")
	} else {
		t.Log("\n==> gopus with DELAY BUFFER is closer to libopus!")
		t.Log("    The issue is the delay compensation.")
	}
}
