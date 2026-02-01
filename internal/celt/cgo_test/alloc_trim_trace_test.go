//go:build trace
// +build trace

// Package cgo traces alloc_trim inputs through the encoder pipeline.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestAllocTrimEncoderTrace traces the exact values through gopus encoder.
func TestAllocTrimEncoderTrace(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
	}

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Logf("Frame config: frameSize=%d, nbBands=%d, LM=%d, shortBlocks=%d", frameSize, nbBands, lm, mode.ShortBlocks)

	// Create encoder
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Step 1: Check delay buffer
	delayComp := celt.DelayCompensation
	t.Logf("\nStep 1: Delay buffer")
	t.Logf("  DelayCompensation = %d samples", delayComp)
	t.Logf("  First frame: delay buffer is zeros")

	// Step 2: DC rejection
	dcRejected := goEnc.ApplyDCReject(pcm64)
	t.Logf("\nStep 2: DC rejection")
	t.Logf("  First 5 DC-rejected samples: %.6f, %.6f, %.6f, %.6f, %.6f",
		dcRejected[0], dcRejected[1], dcRejected[2], dcRejected[3], dcRejected[4])

	// Step 3: Build combined buffer with delay
	combinedLen := delayComp + len(dcRejected)
	combinedBuf := make([]float64, combinedLen)
	// Delay buffer is zeros on first frame
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:frameSize]

	t.Logf("\nStep 3: Combined buffer")
	t.Logf("  Combined length = %d", combinedLen)
	t.Logf("  samplesForFrame length = %d", len(samplesForFrame))
	t.Logf("  First 5 samples (zeros from delay): %.6f, %.6f, %.6f, %.6f, %.6f",
		samplesForFrame[0], samplesForFrame[1], samplesForFrame[2], samplesForFrame[3], samplesForFrame[4])
	t.Logf("  Samples at delay boundary (192-196): %.6f, %.6f, %.6f, %.6f, %.6f",
		samplesForFrame[192], samplesForFrame[193], samplesForFrame[194], samplesForFrame[195], samplesForFrame[196])

	// Step 4: Pre-emphasis
	preemph := goEnc.ApplyPreemphasisWithScaling(samplesForFrame)
	t.Logf("\nStep 4: Pre-emphasis")
	t.Logf("  First 5 pre-emph samples: %.2f, %.2f, %.2f, %.2f, %.2f",
		preemph[0], preemph[1], preemph[2], preemph[3], preemph[4])
	t.Logf("  Pre-emph at delay boundary (192-196): %.2f, %.2f, %.2f, %.2f, %.2f",
		preemph[192], preemph[193], preemph[194], preemph[195], preemph[196])

	// Step 5: MDCT with overlap
	shortBlocks := mode.ShortBlocks // For transient (first frame forced)
	overlap := celt.Overlap
	historyBuf := make([]float64, overlap) // zeros for first frame
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, historyBuf, shortBlocks)

	t.Logf("\nStep 5: MDCT")
	t.Logf("  Using short blocks = %d", shortBlocks)
	t.Logf("  Overlap = %d", overlap)
	t.Logf("  MDCT output length = %d", len(mdctCoeffs))
	t.Logf("  First 10 MDCT coeffs: %.2f, %.2f, %.2f, %.2f, %.2f, %.2f, %.2f, %.2f, %.2f, %.2f",
		mdctCoeffs[0], mdctCoeffs[1], mdctCoeffs[2], mdctCoeffs[3], mdctCoeffs[4],
		mdctCoeffs[5], mdctCoeffs[6], mdctCoeffs[7], mdctCoeffs[8], mdctCoeffs[9])

	// Step 6: Band energies
	energies := goEnc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	t.Logf("\nStep 6: Band energies")
	t.Logf("  All %d bands:", nbBands)
	for i := 0; i < nbBands; i++ {
		t.Logf("    band %2d: %.6f", i, energies[i])
	}

	// Step 7: Spectral tilt
	var diff float64
	end := nbBands
	for i := 0; i < end-1; i++ {
		weight := float64(2 + 2*i - end)
		diff += energies[i] * weight
	}
	diff /= float64(end - 1)

	tiltAdjust := (diff + 1.0) / 6.0
	if tiltAdjust < -2.0 {
		tiltAdjust = -2.0
	}
	if tiltAdjust > 2.0 {
		tiltAdjust = 2.0
	}

	t.Logf("\nStep 7: Spectral tilt")
	t.Logf("  diff = %.6f", diff)
	t.Logf("  tiltAdjust = %.6f (clamped)", tiltAdjust)

	// Step 7b: Check what transient analysis actually computes
	// Build the transient input same as encoder does
	preemphBufSize := overlap
	transientInputLen := (overlap + frameSize)
	transientInput := make([]float64, transientInputLen)
	// For first frame, preemphBuffer is zeros
	copy(transientInput[preemphBufSize:], preemph)

	transientResult := goEnc.TransientAnalysis(transientInput, frameSize+overlap, false)
	t.Logf("\nStep 7b: Transient analysis (raw, before override)")
	t.Logf("  IsTransient = %v", transientResult.IsTransient)
	t.Logf("  MaskMetric = %.2f", transientResult.MaskMetric)
	t.Logf("  TfEstimate (computed) = %.4f", transientResult.TfEstimate)
	t.Logf("  ToneFreq = %.4f", transientResult.ToneFreq)
	t.Logf("  Toneishness = %.4f", transientResult.Toneishness)

	// Step 8: Trim calculation
	effectiveBytes := 159 // For 64kbps CBR
	equivRate := celt.ComputeEquivRate(effectiveBytes, 1, lm, bitrate)

	// Use the COMPUTED tfEstimate instead of forced 0.2
	tfEstimateComputed := transientResult.TfEstimate
	tfEstimateForced := 0.2
	t.Logf("\n  Comparing trim with computed vs forced tfEstimate:")
	t.Logf("    Computed tfEstimate = %.4f", tfEstimateComputed)
	t.Logf("    Forced tfEstimate = %.4f", tfEstimateForced)

	tfEstimate := 0.2 // Forced for first frame (current behavior)

	baseTrim := 5.0
	if equivRate < 64000 {
		baseTrim = 4.0
	} else if equivRate < 80000 {
		baseTrim = 4.0 + float64(equivRate-64000)/16000.0
	}

	finalTrim := baseTrim - tiltAdjust - 2*tfEstimate
	trimIndex := int(math.Floor(0.5 + finalTrim))
	if trimIndex < 0 {
		trimIndex = 0
	}
	if trimIndex > 10 {
		trimIndex = 10
	}

	t.Logf("\nStep 8: Trim calculation")
	t.Logf("  equivRate = %d", equivRate)
	t.Logf("  tfEstimate = %.2f", tfEstimate)
	t.Logf("  baseTrim = %.2f", baseTrim)
	t.Logf("  finalTrim = %.2f - %.2f - %.2f = %.2f", baseTrim, tiltAdjust, 2*tfEstimate, finalTrim)
	t.Logf("  trimIndex = %d", trimIndex)

	// Compare with what AllocTrimAnalysis produces
	normL := goEnc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)
	allocTrim := celt.AllocTrimAnalysis(normL, energies, nbBands, lm, 1, nil, nbBands, tfEstimate, equivRate, 0.0, 0.0)
	t.Logf("\n  AllocTrimAnalysis result = %d", allocTrim)

	if trimIndex != allocTrim {
		t.Errorf("Manual trim calculation (%d) doesn't match AllocTrimAnalysis (%d)", trimIndex, allocTrim)
	}

	// NOW compare with full audio (no delay buffer)
	t.Log("\n=== COMPARISON: Without delay buffer ===")

	// Reset encoder
	goEnc2 := celt.NewEncoder(1)
	goEnc2.Reset()

	// Pre-emphasis directly on raw audio
	preemphFull := goEnc2.ApplyPreemphasisWithScaling(pcm64)
	t.Logf("Full audio first 5 pre-emph samples: %.2f, %.2f, %.2f, %.2f, %.2f",
		preemphFull[0], preemphFull[1], preemphFull[2], preemphFull[3], preemphFull[4])

	// MDCT with zeros overlap (matching first frame conditions)
	historyBuf2 := make([]float64, overlap)
	mdctCoeffsFull := celt.ComputeMDCTWithHistory(preemphFull, historyBuf2, shortBlocks)

	t.Logf("Full audio first 10 MDCT coeffs: %.2f, %.2f, %.2f, %.2f, %.2f, %.2f, %.2f, %.2f, %.2f, %.2f",
		mdctCoeffsFull[0], mdctCoeffsFull[1], mdctCoeffsFull[2], mdctCoeffsFull[3], mdctCoeffsFull[4],
		mdctCoeffsFull[5], mdctCoeffsFull[6], mdctCoeffsFull[7], mdctCoeffsFull[8], mdctCoeffsFull[9])

	// Band energies
	energiesFull := goEnc2.ComputeBandEnergies(mdctCoeffsFull, nbBands, frameSize)
	t.Logf("Full audio band energies (first 10):")
	for i := 0; i < 10; i++ {
		t.Logf("  band %2d: %.6f (with delay: %.6f, diff: %.4f)",
			i, energiesFull[i], energies[i], energiesFull[i]-energies[i])
	}

	// Spectral tilt for full audio
	var diffFull float64
	for i := 0; i < end-1; i++ {
		weight := float64(2 + 2*i - end)
		diffFull += energiesFull[i] * weight
	}
	diffFull /= float64(end - 1)

	tiltAdjustFull := (diffFull + 1.0) / 6.0
	if tiltAdjustFull < -2.0 {
		tiltAdjustFull = -2.0
	}
	if tiltAdjustFull > 2.0 {
		tiltAdjustFull = 2.0
	}

	t.Logf("\nFull audio spectral tilt:")
	t.Logf("  diff = %.6f (with delay: %.6f)", diffFull, diff)
	t.Logf("  tiltAdjust = %.6f (with delay: %.6f)", tiltAdjustFull, tiltAdjust)

	// Trim for full audio
	finalTrimFull := baseTrim - tiltAdjustFull - 2*tfEstimate
	trimIndexFull := int(math.Floor(0.5 + finalTrimFull))
	if trimIndexFull < 0 {
		trimIndexFull = 0
	}
	if trimIndexFull > 10 {
		trimIndexFull = 10
	}

	t.Logf("\nFull audio trim:")
	t.Logf("  finalTrim = %.2f", finalTrimFull)
	t.Logf("  trimIndex = %d (with delay: %d)", trimIndexFull, trimIndex)
}
