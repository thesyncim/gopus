// Package cgo compares intermediate encoder values between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestIntermediateValuesComparison compares pre-emphasis, MDCT, and band energies
func TestIntermediateValuesComparison(t *testing.T) {
	frameSize := 960
	sampleRate := 48000

	// Generate 440Hz sine wave
	pcm := make([]float64, frameSize)
	pcmF32 := make([]float32, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm[i] = val
		pcmF32[i] = float32(val)
	}

	t.Log("=== Intermediate Values Comparison ===")
	t.Log("")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	shortBlocks := mode.ShortBlocks // 8 for transient

	t.Logf("Frame: %d samples, LM=%d, shortBlocks=%d (transient mode)", frameSize, lm, shortBlocks)
	t.Log("")

	// === PRE-EMPHASIS COMPARISON ===
	t.Log("=== Pre-emphasis Comparison ===")

	// Gopus pre-emphasis
	enc := celt.NewEncoder(1)
	enc.Reset()
	gopusPreemph := enc.ApplyPreemphasisWithScaling(pcm)

	// Libopus pre-emphasis
	libPreemph := ApplyLibopusPreemphasis(pcmF32, 0.85)

	t.Log("Pre-emphasis comparison (first 10 samples):")
	t.Log("Sample | Gopus       | Libopus     | Diff")
	t.Log("-------+-------------+-------------+----------")
	maxPreemphDiff := 0.0
	for i := 0; i < 10; i++ {
		diff := math.Abs(gopusPreemph[i] - float64(libPreemph[i]))
		if diff > maxPreemphDiff {
			maxPreemphDiff = diff
		}
		t.Logf("%6d | %11.4f | %11.4f | %8.4f", i, gopusPreemph[i], libPreemph[i], diff)
	}
	t.Logf("Max pre-emphasis difference: %.6f", maxPreemphDiff)
	t.Log("")

	// === BAND ENERGY COMPARISON ===
	t.Log("=== Band Energy Comparison ===")

	// Gopus: compute MDCT and band energies
	mdctCoeffs := celt.ComputeMDCTWithHistory(gopusPreemph, enc.OverlapBuffer(), shortBlocks)
	gopusEnergies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Convert gopus MDCT to float32 for libopus comparison
	mdctF32 := make([]float32, len(mdctCoeffs))
	for i, v := range mdctCoeffs {
		mdctF32[i] = float32(v)
	}

	// Libopus band energies (using gopus MDCT coefficients to isolate energy computation)
	libEnergies := ComputeLibopusBandEnergies(mdctF32, nbBands, frameSize, lm)

	t.Log("Band energy comparison (mean-relative, log2 scale):")
	t.Log("Using SAME MDCT coefficients to isolate energy computation:")
	t.Log("Band | Gopus       | Libopus     | Diff")
	t.Log("-----+-------------+-------------+----------")
	maxEnergyDiff := 0.0
	for band := 0; band < nbBands; band++ {
		diff := math.Abs(gopusEnergies[band] - float64(libEnergies[band]))
		if diff > maxEnergyDiff {
			maxEnergyDiff = diff
		}
		t.Logf("%4d | %11.4f | %11.4f | %8.4f", band, gopusEnergies[band], libEnergies[band], diff)
	}
	t.Logf("Max band energy difference: %.6f (%.2f dB)", maxEnergyDiff, maxEnergyDiff*6)
	t.Log("")

	// === SUMMARY ===
	t.Log("=== Summary ===")
	t.Logf("Pre-emphasis max diff: %.6f", maxPreemphDiff)
	t.Logf("Band energy max diff: %.6f (%.2f dB)", maxEnergyDiff, maxEnergyDiff*6)

	if maxPreemphDiff > 1.0 {
		t.Log("WARNING: Pre-emphasis differs significantly!")
	}
	if maxEnergyDiff > 0.5 {
		t.Log("WARNING: Band energies differ by more than 3 dB!")
	}
}

// TestPreemphasisOnly compares just pre-emphasis in detail
func TestPreemphasisOnly(t *testing.T) {
	frameSize := 960
	sampleRate := 48000

	// Simple test signal
	pcm := make([]float64, frameSize)
	pcmF32 := make([]float32, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm[i] = val
		pcmF32[i] = float32(val)
	}

	t.Log("=== Pre-emphasis Detailed Comparison ===")
	t.Log("")

	// Gopus pre-emphasis
	enc := celt.NewEncoder(1)
	enc.Reset()
	gopus := enc.ApplyPreemphasisWithScaling(pcm)

	// Libopus pre-emphasis
	libopus := ApplyLibopusPreemphasis(pcmF32, 0.85)

	// Also compute manual reference
	manual := make([]float64, frameSize)
	var mem float64 = 0
	coef := 0.85
	for i := 0; i < frameSize; i++ {
		x := pcm[i] * 32768.0
		manual[i] = x - mem
		mem = coef * x
	}

	t.Log("Sample | Input    | Gopus        | Libopus      | Manual       | Gopus-Lib")
	t.Log("-------+----------+--------------+--------------+--------------+----------")
	maxDiff := 0.0
	for i := 0; i < 20; i++ {
		diff := math.Abs(gopus[i] - float64(libopus[i]))
		if diff > maxDiff {
			maxDiff = diff
		}
		t.Logf("%6d | %8.5f | %12.4f | %12.4f | %12.4f | %8.6f",
			i, pcm[i], gopus[i], libopus[i], manual[i], diff)
	}
	t.Log("")
	t.Logf("Max gopus vs libopus difference: %.10f", maxDiff)

	if maxDiff > 1e-3 {
		t.Errorf("Pre-emphasis differs! Max diff: %v", maxDiff)
	} else {
		t.Log("Pre-emphasis MATCHES!")
	}
}

// TestEMeansComparison compares eMeans values
func TestEMeansComparison(t *testing.T) {
	t.Log("=== eMeans Comparison ===")
	t.Log("Band | Gopus eMeans | Libopus eMeans | Diff")
	t.Log("-----+--------------+----------------+------")

	gopusEMeans := celt.GetEMeans()
	maxDiff := 0.0

	for band := 0; band < 21; band++ {
		libEMean := GetLibopusEMeans(band)
		gopusEMean := gopusEMeans[band]
		diff := math.Abs(gopusEMean - float64(libEMean))
		if diff > maxDiff {
			maxDiff = diff
		}

		match := "OK"
		if diff > 0.001 {
			match = "DIFFER"
		}
		t.Logf("%4d | %12.6f | %14.6f | %s", band, gopusEMean, libEMean, match)
	}

	t.Logf("Max eMeans difference: %.10f", maxDiff)

	if maxDiff > 0.001 {
		t.Errorf("eMeans values differ! Max diff: %v", maxDiff)
	} else {
		t.Log("eMeans MATCH!")
	}
}

// TestEBandsComparison compares eBand boundaries
func TestEBandsComparison(t *testing.T) {
	t.Log("=== eBands Comparison ===")

	for lm := 0; lm <= 3; lm++ {
		t.Logf("LM=%d:", lm)

		// Get gopus eBands
		gopusEBands := celt.GetEBands(lm)

		// Get libopus eBands
		nbBands := 21
		libEBands := GetLibopusEBands(lm, nbBands)

		t.Log("Band | Gopus Start | Libopus Start | Match")
		t.Log("-----+-------------+---------------+------")

		allMatch := true
		for band := 0; band <= nbBands && band < len(gopusEBands) && band < len(libEBands); band++ {
			match := "OK"
			if gopusEBands[band] != libEBands[band] {
				match = "DIFFER"
				allMatch = false
			}
			t.Logf("%4d | %11d | %13d | %s", band, gopusEBands[band], libEBands[band], match)
		}

		if !allMatch {
			t.Errorf("eBands differ for LM=%d", lm)
		}
		t.Log("")
	}
}

// TestBandEnergyComputation tests band energy computation in detail
func TestBandEnergyComputation(t *testing.T) {
	frameSize := 960
	sampleRate := 48000

	// Generate 440Hz sine wave
	pcm := make([]float64, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== Band Energy Computation Detail ===")
	t.Log("")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	shortBlocks := mode.ShortBlocks

	// Gopus: compute MDCT and band energies
	enc := celt.NewEncoder(1)
	enc.Reset()
	preemph := enc.ApplyPreemphasisWithScaling(pcm)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)

	// Convert to float32
	mdctF32 := make([]float32, len(mdctCoeffs))
	for i, v := range mdctCoeffs {
		mdctF32[i] = float32(v)
	}

	// Get raw energies (before eMeans subtraction)
	gopusEnergiesRaw := enc.ComputeBandEnergiesRaw(mdctCoeffs, nbBands, frameSize)
	libEnergiesRaw := ComputeLibopusBandEnergiesRaw(mdctF32, nbBands, frameSize, lm)

	t.Log("RAW band energies (log2 amplitude, before eMeans subtraction):")
	t.Log("Band | Gopus Raw   | Libopus Raw | Diff")
	t.Log("-----+-------------+-------------+----------")
	maxRawDiff := 0.0
	for band := 0; band < nbBands; band++ {
		diff := math.Abs(gopusEnergiesRaw[band] - float64(libEnergiesRaw[band]))
		if diff > maxRawDiff {
			maxRawDiff = diff
		}
		t.Logf("%4d | %11.4f | %11.4f | %8.6f", band, gopusEnergiesRaw[band], libEnergiesRaw[band], diff)
	}
	t.Logf("Max raw energy difference: %.10f", maxRawDiff)
	t.Log("")

	// Get mean-relative energies
	gopusEnergies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	libEnergies := ComputeLibopusBandEnergies(mdctF32, nbBands, frameSize, lm)

	t.Log("Mean-relative band energies (after eMeans subtraction):")
	t.Log("Band | Gopus       | Libopus     | Diff")
	t.Log("-----+-------------+-------------+----------")
	maxRelDiff := 0.0
	for band := 0; band < nbBands; band++ {
		diff := math.Abs(gopusEnergies[band] - float64(libEnergies[band]))
		if diff > maxRelDiff {
			maxRelDiff = diff
		}
		t.Logf("%4d | %11.4f | %11.4f | %8.6f", band, gopusEnergies[band], libEnergies[band], diff)
	}
	t.Logf("Max mean-relative energy difference: %.10f", maxRelDiff)

	if maxRawDiff > 0.001 {
		t.Errorf("Raw band energies differ! Max diff: %.6f", maxRawDiff)
	}
	if maxRelDiff > 0.001 {
		t.Errorf("Mean-relative band energies differ! Max diff: %.6f", maxRelDiff)
	}
}
