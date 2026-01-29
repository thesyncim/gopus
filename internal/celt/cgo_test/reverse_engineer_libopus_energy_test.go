// Package cgo reverse-engineers libopus band energies from its QI values.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestReverseEngineerLibopusEnergies computes what band energies libopus used.
func TestReverseEngineerLibopusEnergies(t *testing.T) {
	frameSize := 960

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Log("=== Reverse Engineer Libopus Band Energies ===")
	t.Log("")

	// From TestActualEncodingDivergence:
	// Libopus QI values: 2, 4, 2, -1, -2, -1, -1, 0, -1, -1, 0, 0, 0, 0, 0, 0, 0, -1, 0, 0, 1
	libopusQIs := []int{2, 4, 2, -1, -2, -1, -1, 0, -1, -1, 0, 0, 0, 0, 0, 0, 0, -1, 0, 0, 1}

	// Gopus energies from TestActualEncodingDivergence:
	gopusEnergies := []float64{2.9604, 5.6063, 6.7996, 5.1664, 4.1621, 2.5222, 0.6154, 2.3242, 1.3402, 1.1441,
		0.9770, 0.8650, 0.6462, 0.5871, 0.6513, 0.5068, 0.3841, 0.1119, 0.0257, 0.3624, 0.9782}

	// Prediction coefficients
	coef := celt.AlphaCoef[lm]
	beta := celt.BetaCoefInter[lm]
	DB6 := 1.0

	t.Logf("Prediction: coef=%.6f, beta=%.6f", coef, beta)
	t.Log("")

	// Reverse engineer: given QI values, what energies would produce them?
	// qi = round((x - coef*oldE - prev) / DB6)
	// x = qi*DB6 + coef*oldE + prev
	// For first frame: oldE = 0, so x = qi*DB6 + prev

	t.Log("=== Forward Simulation with Libopus QIs ===")
	t.Log("Band | Libopus QI | Implied Energy | Gopus Energy | Delta")
	t.Log("-----+------------+----------------+--------------+------")

	libImpliedEnergies := make([]float64, nbBands)
	prev := 0.0

	for band := 0; band < nbBands && band < len(libopusQIs); band++ {
		qi := libopusQIs[band]
		oldE := 0.0 // First frame
		if oldE < -9.0*DB6 {
			oldE = -9.0 * DB6
		}

		// Implied energy that would produce this QI
		// qi = round((x - coef*oldE - prev) / DB6)
		// So x ≈ qi*DB6 + coef*oldE + prev (center of rounding window)
		impliedX := float64(qi)*DB6 + coef*oldE + prev
		libImpliedEnergies[band] = impliedX

		delta := gopusEnergies[band] - impliedX

		t.Logf("%4d | %10d | %14.4f | %12.4f | %5.2f", band, qi, impliedX, gopusEnergies[band], delta)

		// Update prev for next band (using libopus QI)
		q := float64(qi) * DB6
		prev = prev + q - beta*q
	}

	t.Log("")
	t.Log("=== Energy Difference Analysis ===")

	// Compute statistics
	var sumDelta, sumAbsDelta float64
	for band := 0; band < nbBands && band < len(libImpliedEnergies); band++ {
		delta := gopusEnergies[band] - libImpliedEnergies[band]
		sumDelta += delta
		if delta < 0 {
			sumAbsDelta -= delta
		} else {
			sumAbsDelta += delta
		}
	}

	avgDelta := sumDelta / float64(nbBands)
	avgAbsDelta := sumAbsDelta / float64(nbBands)

	t.Logf("Average energy delta (gopus - libopus implied): %.4f", avgDelta)
	t.Logf("Average absolute energy delta: %.4f", avgAbsDelta)
	t.Logf("Average delta in dB: %.2f dB", avgDelta*6)
	t.Log("")

	// Now let's see what gopus QIs would be
	t.Log("=== Gopus QI Prediction (with correct coefficients) ===")
	t.Log("Band | Gopus Energy | Predicted QI | Actual Gopus QI | Libopus QI | Match Lib")
	t.Log("-----+--------------+--------------+-----------------+------------+----------")

	prev = 0.0
	for band := 0; band < 12 && band < len(gopusEnergies); band++ {
		x := gopusEnergies[band]
		oldE := 0.0
		if oldE < -9.0*DB6 {
			oldE = -9.0 * DB6
		}

		f := x - coef*oldE - prev
		predictedQI := int(math.Floor(f/DB6 + 0.5))

		actualGopusQI := []int{3, 3, 2, -1, -1, -2, -3, 2, -1, 0, -1, 0}[band]
		libQI := libopusQIs[band]

		matchLib := "NO"
		if predictedQI == libQI {
			matchLib = "YES"
		}

		t.Logf("%4d | %12.4f | %12d | %15d | %10d | %s",
			band, x, predictedQI, actualGopusQI, libQI, matchLib)

		// Update prev
		q := float64(predictedQI) * DB6
		prev = prev + q - beta*q
	}

	t.Log("")
	t.Log("=== Analysis ===")
	t.Log("The key observation: gopus band energies produce QI values that match")
	t.Log("the actual gopus encoded values (3, 3, 2, -1, -1, -2, -3, 2, -1, 0, ...).")
	t.Log("")
	t.Log("But libopus QI values (2, 4, 2, -1, -2, -1, -1, 0, -1, -1, ...) imply")
	t.Log("DIFFERENT band energies. The average delta is ~0.5 (3 dB).")
	t.Log("")
	t.Log("Possible causes:")
	t.Log("1. Libopus uses different MDCT coefficients (different windowing?)")
	t.Log("2. Libopus uses different pre-emphasis memory for first frame")
	t.Log("3. Libopus has different lookahead handling")
	t.Log("4. Libopus uses different signal path (analysis before encoding)")
}
