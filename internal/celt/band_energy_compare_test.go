// Package celt tests band energy computation against libopus.
package celt

import (
	"math"
	"testing"
)

// TestBandEnergyFormula verifies the band energy formula matches libopus.
//
// libopus formula (float path):
//   bandE[i] = sqrt(sum(X[j]^2) + 1e-27)  // compute_band_energies
//   bandLogE[i] = log2(bandE[i]) - eMeans[i]  // amp2Log2
//
// gopus formula:
//   sumSq = 1e-27 + sum(X[j]^2)
//   energy = 0.5 * log2(sumSq)  // = log2(sqrt(sumSq))
//   energy -= eMeans[band] * DB6
//
// Key observation: libopus stores amplitude (sqrt), gopus stores log directly.
// The log2 conversion in gopus should produce: log2(sqrt(sumSq)) = 0.5 * log2(sumSq)
func TestBandEnergyFormula(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	nbBands := 21

	// Generate test MDCT coefficients (440Hz sine)
	pcm := make([]float64, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	enc := NewEncoder(1)
	preemph := enc.ApplyPreemphasisWithScaling(pcm)
	history := make([]float64, Overlap)
	mdctCoeffs := ComputeMDCTWithHistory(preemph, history, 1)

	t.Log("=== Band Energy Formula Comparison ===")
	t.Log("")

	// Compute energies using gopus method
	gopusEnergies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Compute energies using libopus-style method (for comparison)
	libopusStyleEnergies := make([]float64, nbBands)

	for band := 0; band < nbBands; band++ {
		start := ScaledBandStart(band, frameSize)
		end := ScaledBandEnd(band, frameSize)

		if end > len(mdctCoeffs) {
			end = len(mdctCoeffs)
		}

		// libopus: sum = 1e-27 + inner_prod(X, X, N)
		sumSq := 1e-27
		for j := start; j < end; j++ {
			sumSq += mdctCoeffs[j] * mdctCoeffs[j]
		}

		// libopus: bandE = sqrt(sum)
		amplitude := math.Sqrt(sumSq)

		// libopus: celt_log2(bandE) - eMeans[i]
		// celt_log2 for float is: 1.442695040888963387*log(x) = log2(x)
		logE := math.Log2(amplitude)

		// Subtract eMeans (these are already in log2 units, 1.0 = 6dB)
		if band < len(eMeans) {
			logE -= eMeans[band]
		}

		libopusStyleEnergies[band] = logE
	}

	t.Log("Band | gopus        | libopus-style | diff")
	t.Log("-----|--------------|---------------|-------")

	maxDiff := 0.0
	for band := 0; band < nbBands; band++ {
		diff := gopusEnergies[band] - libopusStyleEnergies[band]
		if math.Abs(diff) > maxDiff {
			maxDiff = math.Abs(diff)
		}
		t.Logf("%4d | %12.6f | %12.6f | %+.6f", band, gopusEnergies[band], libopusStyleEnergies[band], diff)
	}

	t.Log("")
	t.Logf("Maximum difference: %.10f", maxDiff)

	// Should be essentially zero (floating point precision)
	if maxDiff > 1e-10 {
		t.Errorf("Band energy formulas differ: maxDiff=%.10f", maxDiff)
	}
}

// TestBandEnergyScaling checks if the energy values produce expected QI values.
// Given that QI = round(f / DB6) where f = x - coef*oldE - prevBandEnergy,
// for the first frame (intra=true) with coef=0 and prevBandEnergy=0,
// we should have QI = round(x / DB6) = round(x) since DB6=1.0.
func TestBandEnergyScaling(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	nbBands := 21

	// Generate test signal
	pcm := make([]float64, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	enc := NewEncoder(1)
	preemph := enc.ApplyPreemphasisWithScaling(pcm)
	history := make([]float64, Overlap)
	mdctCoeffs := ComputeMDCTWithHistory(preemph, history, 1)

	gopusEnergies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("=== Expected QI Values (intra=true, first frame) ===")
	t.Log("")
	t.Log("For intra mode with coef=0 and first band:")
	t.Log("  f = x - 0*oldE - 0 = x")
	t.Log("  qi = round(f / DB6) = round(x)")
	t.Log("")
	t.Log("Band | energy (x)   | round(x) = expected qi")
	t.Log("-----|--------------|------------------------")

	// Compute expected QI values using intra mode prediction
	// coef = 0 (no inter-frame prediction for intra)
	// beta = BetaIntra
	prevBandEnergy := 0.0
	betaIntra := 4915.0 / 32768.0

	expectedQIs := make([]int, nbBands)
	for band := 0; band < nbBands; band++ {
		x := gopusEnergies[band]
		// For intra mode: coef=0, so prediction is just prevBandEnergy
		f := x - prevBandEnergy
		qi := int(math.Floor(f/DB6 + 0.5))
		expectedQIs[band] = qi

		t.Logf("%4d | %12.6f | %d", band, x, qi)

		// Update inter-band predictor
		q := float64(qi) * DB6
		prevBandEnergy = prevBandEnergy + q - betaIntra*q
	}

	t.Log("")
	t.Log("Expected QI sequence (first 12):")
	for i := 0; i < 12 && i < nbBands; i++ {
		t.Logf("  Band %d: qi=%d", i, expectedQIs[i])
	}

	// Compare with Agent 10's finding:
	// libopus QIs: 2, 4, 2, -1, -2, -1, -1, 0, -1, -1, 0, 0, ...
	t.Log("")
	t.Log("Agent 10's observed libopus QIs: 2, 4, 2, -1, -2, -1, -1, 0, -1, -1, 0, 0")
	t.Log("Agent 10's observed gopus QIs:   3, 3, 2, -1, -1, -2, -3, 2, -1, 0, -1, 0")
}

// TestRawBandEnergies checks raw energies without eMeans subtraction.
func TestRawBandEnergies(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	nbBands := 21

	// Generate test signal
	pcm := make([]float64, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	enc := NewEncoder(1)
	preemph := enc.ApplyPreemphasisWithScaling(pcm)
	history := make([]float64, Overlap)
	mdctCoeffs := ComputeMDCTWithHistory(preemph, history, 1)

	// Get raw energies (without eMeans subtraction)
	rawEnergies := enc.ComputeBandEnergiesRaw(mdctCoeffs, nbBands, frameSize)

	// Get normal energies (with eMeans subtraction)
	normalEnergies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("=== Raw vs Normal Energies ===")
	t.Log("")
	t.Log("Band | raw         | normal      | eMeans   | raw-eMeans   | diff from normal")
	t.Log("-----|-------------|-------------|----------|--------------|------------------")

	for band := 0; band < nbBands; band++ {
		rawMinusMeans := rawEnergies[band]
		if band < len(eMeans) {
			rawMinusMeans -= eMeans[band] * DB6
		}
		diff := rawMinusMeans - normalEnergies[band]

		meanVal := 0.0
		if band < len(eMeans) {
			meanVal = eMeans[band]
		}

		t.Logf("%4d | %11.4f | %11.4f | %8.4f | %12.4f | %+.6f",
			band, rawEnergies[band], normalEnergies[band], meanVal, rawMinusMeans, diff)
	}
}
