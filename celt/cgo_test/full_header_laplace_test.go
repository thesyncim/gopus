//go:build trace
// +build trace

// Package cgo tests full header + Laplace encoding comparison.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestFullHeaderLaplaceComparison compares header + Laplace encoding.
func TestFullHeaderLaplaceComparison(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	t.Log("=== Full Header + Laplace Encoding Comparison ===")
	t.Log("")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	shortBlocks := mode.ShortBlocks

	// Create encoder
	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(bitrate)

	// Apply pre-emphasis
	preemph := enc.ApplyPreemphasisWithScaling(samples)

	// MDCT with short blocks (transient mode)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)

	// Band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Logf("Using: shortBlocks=%d, intra=false (inter mode)", shortBlocks)
	t.Log("")

	// Compute QI values for inter mode (intra=false)
	// Using correct prediction coefficients from tables.go
	DB6 := 1.0
	// AlphaCoef[3] = 16384/32768 = 0.5
	// BetaCoefInter[3] = 6554/32768 ≈ 0.2
	coef := 16384.0 / 32768.0 // AlphaCoef[lm] for LM=3
	beta := 6554.0 / 32768.0  // BetaCoefInter[lm] for LM=3

	// Get probability model
	probModel := celt.GetEProbModel()
	prob := probModel[lm][0] // inter mode

	qiValues := make([]int, nbBands)
	fsValues := make([]int, nbBands)
	decayValues := make([]int, nbBands)

	prevBandEnergy := 0.0
	prevEnergy := make([]float64, celt.MaxBands)

	for band := 0; band < nbBands; band++ {
		x := energies[band]

		oldEBand := prevEnergy[band]
		oldE := oldEBand
		if oldE < -9.0*DB6 {
			oldE = -9.0 * DB6
		}

		// Prediction residual
		f := x - coef*oldE - prevBandEnergy

		// Quantize
		qi := int(math.Floor(f/DB6 + 0.5))

		// Get Laplace params
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := int(prob[pi]) << 7
		decay := int(prob[pi+1]) << 6

		qiValues[band] = qi
		fsValues[band] = fs
		decayValues[band] = decay

		// Update state
		q := float64(qi) * DB6
		prevBandEnergy = prevBandEnergy + q - beta*q
	}

	// Show QI values
	t.Log("QI values for bands 0-9:")
	for i := 0; i < 10 && i < nbBands; i++ {
		t.Logf("  Band %d: qi=%d, fs=%d, decay=%d", i, qiValues[i], fsValues[i], decayValues[i])
	}

	// Header flags
	headerBits := []int{0, 0, 1, 0} // silence=0, postfilter=0, transient=1, intra=0
	headerLogps := []int{15, 1, 3, 3}

	// Encode with gopus
	t.Log("")
	t.Log("=== Gopus Encoding ===")
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Encode header
	for i := 0; i < 4; i++ {
		re.EncodeBit(headerBits[i], uint(headerLogps[i]))
	}
	t.Logf("After header: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode Laplace values
	enc.SetRangeEncoder(re)
	for band := 0; band < nbBands; band++ {
		qi := qiValues[band]
		fs := fsValues[band]
		decay := decayValues[band]
		_ = enc.TestEncodeLaplace(qi, fs, decay)
	}
	t.Logf("After Laplace: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	gopusBytes := re.Done()
	t.Logf("Gopus bytes: %02X", gopusBytes[:minIntFullHL(10, len(gopusBytes))])

	// Encode with libopus using TraceHeaderPlusLaplace
	t.Log("")
	t.Log("=== Libopus Encoding ===")
	libStates, libQiValues, libBytes := TraceHeaderPlusLaplace(
		headerBits, headerLogps, qiValues, fsValues, decayValues)

	if libStates != nil && len(libStates) > 4 {
		t.Logf("After header: rng=0x%08X, val=0x%08X, tell=%d",
			libStates[4].Rng, libStates[4].Val, libStates[4].Tell)
	}
	if libStates != nil && len(libStates) > 4+nbBands {
		lastIdx := 4 + nbBands
		t.Logf("After Laplace: rng=0x%08X, val=0x%08X, tell=%d",
			libStates[lastIdx].Rng, libStates[lastIdx].Val, libStates[lastIdx].Tell)
	}
	t.Logf("Libopus bytes: %02X", libBytes[:minIntFullHL(10, len(libBytes))])

	// Compare QI values (should match since we computed them)
	t.Log("")
	t.Log("=== QI Value Comparison ===")
	qiMatch := true
	for i := 0; i < nbBands && i < len(libQiValues); i++ {
		if qiValues[i] != libQiValues[i] {
			t.Logf("  Band %d: gopus qi=%d, libopus qi=%d - DIFFER", i, qiValues[i], libQiValues[i])
			qiMatch = false
		}
	}
	if qiMatch {
		t.Log("All QI values match")
	}

	// Compare bytes
	t.Log("")
	t.Log("=== Byte Comparison ===")
	minLen := len(gopusBytes)
	if len(libBytes) < minLen {
		minLen = len(libBytes)
	}

	divergeIdx := -1
	for i := 0; i < minLen; i++ {
		if gopusBytes[i] != libBytes[i] {
			divergeIdx = i
			break
		}
	}

	if divergeIdx == -1 && len(gopusBytes) == len(libBytes) {
		t.Log("All bytes MATCH!")
	} else {
		if divergeIdx == -1 {
			divergeIdx = minLen
		}
		t.Logf("Bytes diverge at index %d", divergeIdx)
		for i := 0; i < 5 && i < minLen; i++ {
			match := "MATCH"
			if gopusBytes[i] != libBytes[i] {
				match = "DIFFER"
			}
			t.Logf("  [%d]: gopus=0x%02X, libopus=0x%02X - %s", i, gopusBytes[i], libBytes[i], match)
		}
	}
}

func minIntFullHL(a, b int) int {
	if a < b {
		return a
	}
	return b
}
