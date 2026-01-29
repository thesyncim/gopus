package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestNormalizationDebug(t *testing.T) {
	frameSize := 960

	// Simple coefficients with known values
	mdctCoeffs := make([]float64, frameSize)
	// Put unit value in first few bands
	for i := 0; i < 20; i++ {
		mdctCoeffs[i] = 1.0
	}

	enc := celt.NewEncoder(1)
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands

	// Debug: show band boundaries
	t.Log("=== Band Boundaries ===")
	for band := 0; band < 5; band++ {
		start := celt.ScaledBandStart(band, frameSize)
		end := celt.ScaledBandEnd(band, frameSize)
		width := celt.ScaledBandWidth(band, frameSize)
		t.Logf("Band %d: start=%d, end=%d, width=%d, EBands[%d]=%d, EBands[%d]=%d",
			band, start, end, width, band, celt.EBands[band], band+1, celt.EBands[band+1])
	}

	// Compute energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("\n=== Energies (first 5 bands) ===")
	for band := 0; band < 5; band++ {
		start := celt.ScaledBandStart(band, frameSize)
		end := celt.ScaledBandEnd(band, frameSize)

		// Manually compute sumSq
		sumSq := 1e-27
		for i := start; i < end && i < len(mdctCoeffs); i++ {
			sumSq += mdctCoeffs[i] * mdctCoeffs[i]
		}
		expectedEnergy := 0.5 * math.Log2(sumSq)
		if band < len(celt.EMeans) {
			expectedEnergy -= celt.EMeans[band] * celt.DB6
		}

		t.Logf("Band %d: sumSq=%.6f, log2(sqrt(sumSq))=%.6f, energy=%.6f, expected=%.6f",
			band, sumSq, 0.5*math.Log2(sumSq), energies[band], expectedEnergy)
	}

	// Normalize
	norm := enc.NormalizeBandsToArray(mdctCoeffs, energies, nbBands, frameSize)

	t.Log("\n=== Normalization Check (first 5 bands) ===")
	offset := 0
	for band := 0; band < 5; band++ {
		n := celt.ScaledBandWidth(band, frameSize)
		if n <= 0 {
			continue
		}

		// Compute magnitude
		var mag float64
		for i := 0; i < n && offset+i < len(norm); i++ {
			mag += norm[offset+i] * norm[offset+i]
		}
		mag = math.Sqrt(mag)

		// What gain was used?
		eVal := energies[band]
		if band < len(celt.EMeans) {
			eVal += celt.EMeans[band] * celt.DB6
		}
		gain := math.Exp2(eVal / celt.DB6)

		t.Logf("Band %d [offset %d, n=%d]: energy=%.4f, eVal=%.4f, gain=%.6f, magnitude=%.6f",
			band, offset, n, energies[band], eVal, gain, mag)

		// Show first few coefficients
		for i := 0; i < n && i < 3 && offset+i < len(norm); i++ {
			origVal := 0.0
			if offset+i < len(mdctCoeffs) {
				origVal = mdctCoeffs[offset+i]
			}
			t.Logf("    [%d] orig=%.6f, norm=%.6f, orig/gain=%.6f",
				offset+i, origVal, norm[offset+i], origVal/gain)
		}

		offset += n
	}
}
