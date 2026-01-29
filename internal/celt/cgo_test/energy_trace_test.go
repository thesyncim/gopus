package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestEnergyEncodingTrace(t *testing.T) {
	sampleRate := 48000
	frameSize := 960

	// Simple single-frequency signal
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)

	// Apply pre-emphasis like the encoder does
	preemph := enc.ApplyPreemphasis(pcm)

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	// Compute MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, make([]float64, 120), 1)

	// Compute band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("=== Band Energies (dB scale) ===")
	for i := 0; i < nbBands && i < len(energies); i++ {
		freqStart := float64(celt.EBands[i]) * float64(sampleRate) / float64(frameSize)
		freqEnd := float64(celt.EBands[i+1]) * float64(sampleRate) / float64(frameSize)
		t.Logf("  Band %2d [%5.0f - %5.0f Hz]: %.2f dB", i, freqStart, freqEnd, energies[i])
	}

	// Expected: 440Hz signal should have most energy in lower bands
	// 440Hz / (48000/960) = 440/50 = 8.8 -> band ~0-1

	// Now let's trace what happens when we normalize the bands
	t.Log("\n=== Normalized MDCT Coefficients (first band) ===")
	normCoeffs := enc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)

	// Show first band's normalized coefficients
	bandEnd := celt.EBands[1]
	t.Logf("First band has %d coefficients:", bandEnd)
	for i := 0; i < bandEnd && i < len(normCoeffs); i++ {
		t.Logf("  [%d] = %.6f", i, normCoeffs[i])
	}

	// Compute magnitude of normalized coefficients (should be ~1.0)
	var normMag float64
	for i := 0; i < bandEnd; i++ {
		normMag += normCoeffs[i] * normCoeffs[i]
	}
	normMag = math.Sqrt(normMag)
	t.Logf("\nFirst band normalized magnitude: %.6f (should be ~1.0)", normMag)

	// Check a few more bands
	t.Log("\n=== Band Magnitudes After Normalization ===")
	for b := 0; b < 5 && b < nbBands; b++ {
		bStart := celt.EBands[b]
		bEnd := celt.EBands[b+1]
		var mag float64
		for i := bStart; i < bEnd && i < len(normCoeffs); i++ {
			mag += normCoeffs[i] * normCoeffs[i]
		}
		mag = math.Sqrt(mag)
		t.Logf("  Band %d [%d:%d]: magnitude = %.6f", b, bStart, bEnd, mag)
	}
}
