// Package cgo tests whether libopus applies DC rejection for float input
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestDCRejectComparison compares DC reject behavior.
func TestDCRejectComparison(t *testing.T) {
	// Generate 440Hz sine wave
	frameSize := 960
	sampleRate := 48000
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	// Apply gopus DC rejection
	enc := celt.NewEncoder(1)
	enc.Reset()
	dcRejected := enc.ApplyDCReject(pcm64)

	// Compare
	t.Log("=== DC Rejection Analysis ===")
	t.Log("")
	t.Log("Sample | Original   | After DC Reject | Difference")
	t.Log("-------+------------+-----------------+------------")

	maxDiff := 0.0
	for i := 0; i < 20; i++ {
		diff := dcRejected[i] - pcm64[i]
		if math.Abs(diff) > math.Abs(maxDiff) {
			maxDiff = diff
		}
		t.Logf("%6d | %+10.7f | %+15.7f | %+11.9f", i, pcm64[i], dcRejected[i], diff)
	}

	// Check samples in the middle
	t.Log("")
	t.Log("Middle samples:")
	for i := 100; i < 110; i++ {
		diff := dcRejected[i] - pcm64[i]
		t.Logf("%6d | %+10.7f | %+15.7f | %+11.9f", i, pcm64[i], dcRejected[i], diff)
	}

	// The DC rejection filter accumulates state, so by mid-frame it has an effect
	// For a pure sine wave (no DC), the filter SHOULD have minimal effect
	// But with coef=0.00039375, the memory integrates the signal slightly

	t.Log("")
	t.Logf("Max difference: %+.9f", maxDiff)

	// The issue: for a pure sine wave, DC rejection should do almost nothing
	// But we see differences of ~0.00036, which is the coef value!
	// This is because the filter is: out[i] = x[i] - m, m = coef*x[i] + (1-coef)*m
	// After many samples, m approaches the local average of x, which for a sine is ~0
	// But in the first few samples, m is still "catching up"

	// KEY INSIGHT: The difference at sample 100 should be ~= integrated sine * coef
	// For 440Hz at 48kHz, one cycle is ~109 samples
	// After 100 samples (almost one cycle), the integral of sine ≈ 0
	// So the DC rejection difference should be small

	// Actually let's compute what libopus dc_reject would do
	// It uses: m = coef*x + VERY_SMALL + (1-coef)*m
	// where VERY_SMALL = 1e-20
	t.Log("")
	t.Log("=== Checking if DC rejection is the cause ===")

	// Without DC rejection
	encNoDC := celt.NewEncoder(1)
	encNoDC.Reset()
	preemphNoDC := encNoDC.ApplyPreemphasisWithScaling(pcm64)

	// With DC rejection (what EncodeFrame does)
	encWithDC := celt.NewEncoder(1)
	encWithDC.Reset()
	dcRejectedAgain := encWithDC.ApplyDCReject(pcm64)
	preemphWithDC := encWithDC.ApplyPreemphasisWithScaling(dcRejectedAgain)

	t.Log("Pre-emphasis comparison:")
	t.Log("Sample | Without DC | With DC    | Difference")
	for i := 0; i < 10; i++ {
		diff := preemphWithDC[i] - preemphNoDC[i]
		t.Logf("%6d | %+10.2f | %+10.2f | %+10.4f", i, preemphNoDC[i], preemphWithDC[i], diff)
	}
	for i := 100; i < 105; i++ {
		diff := preemphWithDC[i] - preemphNoDC[i]
		t.Logf("%6d | %+10.2f | %+10.2f | %+10.4f", i, preemphNoDC[i], preemphWithDC[i], diff)
	}

	// Now compute MDCT from both
	mode := celt.GetModeConfig(frameSize)
	shortBlocks := mode.ShortBlocks

	mdctNoDC := celt.ComputeMDCTWithHistory(preemphNoDC, make([]float64, 120), shortBlocks)
	mdctWithDC := celt.ComputeMDCTWithHistory(preemphWithDC, make([]float64, 120), shortBlocks)

	t.Log("")
	t.Log("Band 0 MDCT comparison:")
	M := 1 << mode.LM
	for i := 0; i < M; i++ {
		diff := mdctWithDC[i] - mdctNoDC[i]
		t.Logf("Coeff %d: noDC=%+.4f withDC=%+.4f diff=%+.4f", i, mdctNoDC[i], mdctWithDC[i], diff)
	}

	// The question is: does libopus apply dc_reject for the float path?
	// Looking at opus_encoder.c, dc_reject is called BEFORE celt_encode_with_ec
	// on line 2008. So YES, libopus applies DC rejection.
	//
	// BUT - libopus also uses a DELAY BUFFER which shifts the samples by
	// delay_compensation samples (192 at 48kHz). This means the first frame
	// that gets encoded actually contains zeros + start of input.

	t.Log("")
	t.Log("=== Checking delay buffer effect ===")
	t.Log("libopus uses delay_compensation = Fs/250 = 192 samples at 48kHz")
	t.Log("For first frame, libopus encodes: [zeros] + [first 768 samples of input]")
	t.Log("gopus currently encodes: [first 960 samples of input]")
	t.Log("")
	t.Log("This delay buffer mismatch could explain the MDCT differences!")
}
