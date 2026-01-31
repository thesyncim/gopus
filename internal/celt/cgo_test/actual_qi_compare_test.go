// Package cgo compares actual QI values from EncodeCoarseEnergy with test computation.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestActualQICompare compares QI values from actual encoding vs test computation.
func TestActualQICompare(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	t.Log("=== Actual QI Value Comparison ===")
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

	// Initialize range encoder
	targetBits := bitrate * frameSize / sampleRate
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Encode header flags (to match actual encoding state)
	re.EncodeBit(0, 15) // silence=0
	re.EncodeBit(0, 1)  // postfilter=0
	re.EncodeBit(1, 3)  // transient=1
	re.EncodeBit(0, 3)  // intra=0

	t.Logf("After header: tell=%d, budget=%d", re.Tell(), targetBits)

	// Set up encoder for coarse energy
	enc.SetRangeEncoder(re)
	enc.SetFrameBitsForTest(targetBits)

	// Encode coarse energy (inter mode, intra=false)
	intra := false
	quantizedEnergies := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	// Get the actual bytes
	actualBytes := re.Done()

	t.Log("")
	t.Log("=== Actual Encoding Results ===")
	t.Logf("Actual bytes: %02X", actualBytes[:minIntAQI(10, len(actualBytes))])

	// Show energies and quantized values
	t.Log("")
	t.Log("=== Energy Analysis ===")
	t.Log("Band | Input     | Quantized | QI (approx)")
	t.Log("-----+-----------+-----------+-------------")

	// Compute approximate QI values from quantized energies
	DB6 := 1.0
	alphaCoef := []float64{0.75, 0.822727, 0.857143, 0.875}
	coef := alphaCoef[lm]
	prevBandE := 0.0

	for i := 0; i < 10 && i < len(energies); i++ {
		q := quantizedEnergies[i]

		// Approximate qi from quantized energy
		// quantized = coef*oldE + prev + qi*DB6
		// Since oldE=0 for first frame and prev changes:
		qiApprox := (q - prevBandE) / DB6

		t.Logf("%4d | %9.4f | %9.4f | %11.2f", i, energies[i], q, qiApprox)

		// Update inter-band predictor (simplified)
		qi := int(math.Floor(qiApprox + 0.5))
		qVal := float64(qi) * DB6
		beta := 0.132813 // betaCoefInter[3] for lm=3
		prevBandE = prevBandE + qVal - beta*qVal
	}

	// Now compute what the test would produce
	t.Log("")
	t.Log("=== Test Computation ===")
	testQIs := make([]int, nbBands)
	testPrevBandE := 0.0

	probModel := celt.GetEProbModel()
	prob := probModel[lm][0] // inter mode

	for band := 0; band < nbBands; band++ {
		x := energies[band]

		// Previous frame energy = 0 (first frame)
		oldEBand := 0.0 // First frame, prevEnergy is 0
		oldE := oldEBand
		minEnergy := -9.0 * DB6
		if oldE < minEnergy {
			oldE = minEnergy // This won't trigger since 0 > -9
		}

		// Prediction residual
		f := x - coef*oldE - testPrevBandE

		// Quantize
		qi := int(math.Floor(f/DB6 + 0.5))

		testQIs[band] = qi

		// Get Laplace params
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		_ = int(prob[pi]) << 7
		_ = int(prob[pi+1]) << 6

		// Update state
		q := float64(qi) * DB6
		beta := 0.132813
		testPrevBandE = testPrevBandE + q - beta*q
	}

	t.Log("Test QI values for bands 0-9:")
	for i := 0; i < 10 && i < nbBands; i++ {
		t.Logf("  Band %d: testQI=%d", i, testQIs[i])
	}

	// Compare with expected from libopus
	t.Log("")
	t.Log("=== Comparison with libopus ===")

	// Encode same QI values through libopus
	fsValues := make([]int, nbBands)
	decayValues := make([]int, nbBands)
	for band := 0; band < nbBands; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fsValues[band] = int(prob[pi]) << 7
		decayValues[band] = int(prob[pi+1]) << 6
	}

	headerBits := []int{0, 0, 1, 0}
	headerLogps := []int{15, 1, 3, 3}

	libStates, libQIs, libBytes := TraceHeaderPlusLaplace(
		headerBits, headerLogps, testQIs, fsValues, decayValues)

	if libBytes != nil {
		t.Logf("Libopus bytes: %02X", libBytes[:minIntAQI(10, len(libBytes))])
	}

	// Compare bytes
	t.Log("")
	t.Log("Byte comparison (first 5):")
	for i := 0; i < 5 && i < len(actualBytes) && i < len(libBytes); i++ {
		match := "MATCH"
		if actualBytes[i] != libBytes[i] {
			match = "DIFFER"
		}
		t.Logf("  [%d]: actual=0x%02X, libopus=0x%02X - %s", i, actualBytes[i], libBytes[i], match)
	}

	// Show if QI values were modified by libopus
	t.Log("")
	t.Log("QI modification by Laplace encoding:")
	for i := 0; i < 10 && i < len(testQIs) && i < len(libQIs); i++ {
		if testQIs[i] != libQIs[i] {
			t.Logf("  Band %d: input=%d, output=%d (MODIFIED)", i, testQIs[i], libQIs[i])
		}
	}

	_ = libStates // Used for debugging
}

func minIntAQI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
