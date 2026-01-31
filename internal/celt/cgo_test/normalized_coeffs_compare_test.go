// Package cgo provides CGO wrappers for libopus comparison tests.
// This test compares normalized coefficients used for TF analysis.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestNormalizedCoeffsMatch tests that Go normalized coefficients match libopus.
func TestNormalizedCoeffsMatch(t *testing.T) {
	// Generate test MDCT coefficients (simulating typical encoder output)
	frameSize := 960
	lm := 3
	nbBands := 21

	// Create test signal - 440Hz sine wave
	samples := make([]float64, frameSize)
	freq := 440.0
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
	}

	// Apply pre-emphasis
	encoder := celt.NewEncoder(1)
	preemph := encoder.ApplyPreemphasisWithScaling(samples)

	// Compute MDCT
	overlap := celt.Overlap
	input := make([]float64, len(preemph)+overlap)
	copy(input[overlap:], preemph)
	mdctCoeffs := celt.MDCT(input)

	// Convert to float32 for C comparison
	mdctFloat32 := make([]float32, len(mdctCoeffs))
	for i, v := range mdctCoeffs {
		mdctFloat32[i] = float32(v)
	}

	// --- LIBOPUS PATH ---
	// Compute LINEAR band energies (like libopus compute_band_energies)
	bandELinear := ComputeLibopusBandEnergyLinear(mdctFloat32, nbBands, frameSize, lm)

	// Normalize using libopus formula: X[j] = freq[j] / bandE[i]
	normalizedLibopus := NormaliseLibopusBands(mdctFloat32, bandELinear, frameSize, nbBands, lm)

	// --- GO PATH ---
	// Compute band energies using Go (returns log2 scale, mean-relative)
	bandEGo := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Normalize using Go formula
	normalizedGo := encoder.NormalizeBandsToArray(mdctCoeffs, bandEGo, nbBands, frameSize)

	// Compare normalized coefficients band by band
	t.Log("Comparing normalized coefficients between libopus and Go:")
	t.Log("Format: Band N: [start-end) Max|Diff| Libopus[0..3] vs Go[0..3]")

	const tolerance = 1e-4 // Allow small floating point differences
	totalDiffs := 0
	maxDiff := 0.0
	maxDiffBand := -1

	for band := 0; band < nbBands; band++ {
		bandStart := celt.ScaledBandStart(band, frameSize)
		bandEnd := celt.ScaledBandEnd(band, frameSize)
		if bandEnd > frameSize {
			bandEnd = frameSize
		}

		// Find max diff in this band
		bandMaxDiff := 0.0
		for i := bandStart; i < bandEnd; i++ {
			libVal := float64(normalizedLibopus[i])
			goVal := normalizedGo[i]
			diff := math.Abs(libVal - goVal)
			if diff > bandMaxDiff {
				bandMaxDiff = diff
			}
		}

		if bandMaxDiff > maxDiff {
			maxDiff = bandMaxDiff
			maxDiffBand = band
		}

		// Show first few values
		samplesStart := minIntLocal(bandStart+4, bandEnd)
		t.Logf("Band %2d [%3d-%3d): MaxDiff=%.6f Lib=[%.4f,%.4f,%.4f,%.4f] Go=[%.4f,%.4f,%.4f,%.4f]",
			band, bandStart, bandEnd, bandMaxDiff,
			normalizedLibopus[bandStart], safeGet32Local(normalizedLibopus, bandStart+1, samplesStart),
			safeGet32Local(normalizedLibopus, bandStart+2, samplesStart), safeGet32Local(normalizedLibopus, bandStart+3, samplesStart),
			normalizedGo[bandStart], safeGet64Local(normalizedGo, bandStart+1, samplesStart),
			safeGet64Local(normalizedGo, bandStart+2, samplesStart), safeGet64Local(normalizedGo, bandStart+3, samplesStart))

		// Compare libopus linear bandE to Go log->linear conversion
		goLinearE := math.Exp2(bandEGo[band] + celt.GetEMeansBand(band))
		libLinearE := bandELinear[band]
		eDiff := math.Abs(float64(libLinearE) - goLinearE)
		if eDiff > 1e-3 {
			t.Logf("  -> BandE mismatch: Lib=%.6f Go=%.6f (diff=%.6f)", libLinearE, goLinearE, eDiff)
		}

		if bandMaxDiff > tolerance {
			totalDiffs++
		}
	}

	t.Logf("\nMax overall diff: %.6f in band %d", maxDiff, maxDiffBand)

	if totalDiffs > 0 {
		t.Errorf("Found %d bands with differences > %.6f", totalDiffs, tolerance)
	}
}

func safeGet32Local(arr []float32, idx, limit int) float32 {
	if idx < limit && idx < len(arr) {
		return arr[idx]
	}
	return 0
}

func safeGet64Local(arr []float64, idx, limit int) float64 {
	if idx < limit && idx < len(arr) {
		return arr[idx]
	}
	return 0
}

func minIntLocal(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestNormalizedCoeffsForTFAnalysis specifically tests the input to TF analysis.
func TestNormalizedCoeffsForTFAnalysis(t *testing.T) {
	// Test with frame parameters matching typical encoder settings
	frameSize := 960
	lm := 3
	nbBands := 21

	// Create varied test signal (mix of frequencies)
	samples := make([]float64, frameSize)
	for i := range samples {
		t_sec := float64(i) / 48000.0
		samples[i] = 0.3*math.Sin(2.0*math.Pi*440.0*t_sec) +
			0.2*math.Sin(2.0*math.Pi*880.0*t_sec) +
			0.1*math.Sin(2.0*math.Pi*1760.0*t_sec)
	}

	encoder := celt.NewEncoder(1)
	preemph := encoder.ApplyPreemphasisWithScaling(samples)

	overlap := celt.Overlap
	input := make([]float64, len(preemph)+overlap)
	copy(input[overlap:], preemph)
	mdctCoeffs := celt.MDCT(input)

	mdctFloat32 := make([]float32, len(mdctCoeffs))
	for i, v := range mdctCoeffs {
		mdctFloat32[i] = float32(v)
	}

	// Libopus path
	bandELinear := ComputeLibopusBandEnergyLinear(mdctFloat32, nbBands, frameSize, lm)
	normalizedLibopus := NormaliseLibopusBands(mdctFloat32, bandELinear, frameSize, nbBands, lm)

	// Go path
	bandEGo := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	normalizedGo := encoder.NormalizeBandsToArray(mdctCoeffs, bandEGo, nbBands, frameSize)

	// Compute per-band L1 metric (used in TF analysis)
	t.Log("Per-band L1 metric comparison (used for TF analysis):")

	for band := 0; band < nbBands; band++ {
		bandStart := celt.ScaledBandStart(band, frameSize)
		bandEnd := celt.ScaledBandEnd(band, frameSize)
		if bandEnd > frameSize {
			bandEnd = frameSize
		}

		var L1Lib, L1Go float64
		for i := bandStart; i < bandEnd; i++ {
			L1Lib += math.Abs(float64(normalizedLibopus[i]))
			L1Go += math.Abs(normalizedGo[i])
		}

		relDiff := 0.0
		if L1Lib > 1e-10 {
			relDiff = math.Abs(L1Lib-L1Go) / L1Lib * 100
		}

		t.Logf("Band %2d: L1_Lib=%.4f L1_Go=%.4f RelDiff=%.2f%%", band, L1Lib, L1Go, relDiff)

		// L1 metrics should be very close (within 0.1%)
		if relDiff > 0.1 {
			t.Errorf("Band %d L1 metric differs by %.2f%% (Lib=%.4f, Go=%.4f)", band, relDiff, L1Lib, L1Go)
		}
	}
}
