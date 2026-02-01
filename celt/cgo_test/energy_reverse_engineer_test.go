//go:build cgo_libopus
// +build cgo_libopus

// Package cgo reverse-engineers band energies from QI values.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestReverseEngineerEnergies computes what band energies would produce the observed QI values.
func TestReverseEngineerEnergies(t *testing.T) {
	frameSize := 960
	sampleRate := 48000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== Reverse Engineer Band Energies from QI Values ===")
	t.Log("")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	shortBlocks := mode.ShortBlocks // 8 for transient

	// Get correct prediction coefficients
	coef := celt.AlphaCoef[lm]     // 0.5 for LM=3
	beta := celt.BetaCoefInter[lm] // ~0.2 for LM=3
	DB6 := 1.0

	t.Logf("Frame: %d samples, LM=%d, shortBlocks=%d", frameSize, lm, shortBlocks)
	t.Logf("Coefficients: alpha=%.6f, beta=%.6f", coef, beta)
	t.Log("")

	// Gopus QI values (from TestDecodeQIFromBothPackets)
	gopusQIs := []int{3, 3, 2, -1, -1, -2, -3, 2, -1, 0, -1, 0, 0, 0, 0, 0, 0, -1, 0, 0, 1}
	libopusQIs := []int{2, 4, 2, -1, -2, -1, -1, 0, -1, -1, 0, 0, 0, 0, 0, 0, 0, -1, 0, 0, 1}

	// Compute gopus band energies
	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(64000)

	preemph := enc.ApplyPreemphasisWithScaling(pcm64)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)
	gopusEnergies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("=== Gopus Band Energies ===")
	for i := 0; i < 10; i++ {
		t.Logf("  Band %d: %.4f", i, gopusEnergies[i])
	}

	// Forward compute: verify QI values from gopus energies
	t.Log("")
	t.Log("=== Forward QI Computation (from gopus energies) ===")
	t.Log("Using gopus energies with correct coefficients:")
	t.Log("Band | Energy | f (resid) | Computed QI | Actual Gopus QI | Match")
	t.Log("-----+--------+-----------+-------------+-----------------+------")

	prevBandEnergy := 0.0
	for band := 0; band < 10; band++ {
		x := gopusEnergies[band]
		oldE := 0.0 // First frame, prevEnergy = 0
		minEnergy := -9.0 * DB6
		if oldE < minEnergy {
			oldE = minEnergy
		}

		f := x - coef*oldE - prevBandEnergy
		qi := int(math.Floor(f/DB6 + 0.5))

		match := "YES"
		if qi != gopusQIs[band] {
			match = "NO"
		}
		t.Logf("%4d | %6.3f | %9.4f | %11d | %15d | %s",
			band, x, f, qi, gopusQIs[band], match)

		// Update predictor
		q := float64(qi) * DB6
		prevBandEnergy = prevBandEnergy + q - beta*q
	}

	// Reverse compute: what energies would produce libopus QIs?
	t.Log("")
	t.Log("=== Reverse Engineering: What energies produce libopus QIs? ===")
	t.Log("Band | Libopus QI | Required f | Required Energy | Gopus Energy | Delta")
	t.Log("-----+------------+------------+-----------------+--------------+-------")

	libPrevBandEnergy := 0.0
	for band := 0; band < 10; band++ {
		qi := libopusQIs[band]
		oldE := 0.0
		if oldE < -9.0*DB6 {
			oldE = -9.0 * DB6
		}

		// qi = round(f / DB6) => f ≈ qi * DB6
		// f = x - coef*oldE - prev => x = f + coef*oldE + prev
		requiredF := float64(qi) * DB6
		requiredEnergy := requiredF + coef*oldE + libPrevBandEnergy

		delta := requiredEnergy - gopusEnergies[band]

		t.Logf("%4d | %10d | %10.4f | %15.4f | %12.4f | %6.3f",
			band, qi, requiredF, requiredEnergy, gopusEnergies[band], delta)

		// Update predictor
		q := float64(qi) * DB6
		libPrevBandEnergy = libPrevBandEnergy + q - beta*q
	}

	// Check if there's a systematic offset
	t.Log("")
	t.Log("=== Analysis ===")

	sumDelta := 0.0
	for band := 0; band < nbBands && band < len(libopusQIs); band++ {
		qi := libopusQIs[band]
		oldE := 0.0
		if oldE < -9.0*DB6 {
			oldE = -9.0 * DB6
		}

		// Reset prev for this calculation (simplified)
		prev := 0.0
		for b := 0; b < band; b++ {
			q := float64(libopusQIs[b]) * DB6
			prev = prev + q - beta*q
		}

		requiredF := float64(qi) * DB6
		requiredEnergy := requiredF + coef*oldE + prev

		sumDelta += gopusEnergies[band] - requiredEnergy
	}
	avgDelta := sumDelta / float64(nbBands)
	t.Logf("Average energy delta (gopus - required for libopus): %.4f", avgDelta)
	t.Logf("This corresponds to ~%.1f dB difference", avgDelta*6)
}
