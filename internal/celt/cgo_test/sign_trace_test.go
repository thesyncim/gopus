package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestSignTraceEncoding(t *testing.T) {
	sampleRate := 48000
	frameSize := 960

	// Generate simple sine wave
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== Sign Trace Through Encoding Pipeline ===")
	t.Logf("Original samples [400:405]: %.4f, %.4f, %.4f, %.4f, %.4f",
		pcm[400], pcm[401], pcm[402], pcm[403], pcm[404])
	t.Logf("Original sign at 400: %s", signStr(pcm[400]))

	// Step 1: Pre-emphasis
	enc := celt.NewEncoder(1)
	preemph := enc.ApplyPreemphasis(pcm)
	t.Logf("\nAfter pre-emphasis [400:405]: %.6f, %.6f, %.6f, %.6f, %.6f",
		preemph[400], preemph[401], preemph[402], preemph[403], preemph[404])
	t.Logf("Pre-emphasis sign at 400: %s", signStr(preemph[400]))

	// Step 2: MDCT
	history := make([]float64, 120)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, history, 1)
	t.Logf("\nMDCT coeffs [0:10]: %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f",
		mdctCoeffs[0], mdctCoeffs[1], mdctCoeffs[2], mdctCoeffs[3], mdctCoeffs[4],
		mdctCoeffs[5], mdctCoeffs[6], mdctCoeffs[7], mdctCoeffs[8], mdctCoeffs[9])

	// Check MDCT DC component sign
	mdctDCSign := signStr(mdctCoeffs[0])
	t.Logf("MDCT DC coefficient sign: %s (value: %.6f)", mdctDCSign, mdctCoeffs[0])

	// Compute band 2 energy (where 440Hz should be)
	mode := celt.GetModeConfig(frameSize)
	lm := mode.LM
	band2Start := celt.EBands[2] << lm
	band2End := celt.EBands[3] << lm
	t.Logf("\nBand 2 (440Hz region) MDCT coeffs [%d:%d]:", band2Start, band2End)
	for i := band2Start; i < band2End && i < len(mdctCoeffs); i++ {
		t.Logf("  [%d] = %.6f (%s)", i, mdctCoeffs[i], signStr(mdctCoeffs[i]))
	}

	// Step 3: Normalization
	t.Log("\n=== Testing normalization sign preservation ===")

	// Create test band with known values
	testBand := []float64{1.0, 2.0, -3.0, 4.0}
	testEnergy := 0.0
	for _, v := range testBand {
		testEnergy += v * v
	}
	testEnergy = math.Sqrt(testEnergy)
	t.Logf("Test band before normalize: %v, energy=%.4f", testBand, testEnergy)

	// Normalize
	for i := range testBand {
		testBand[i] /= testEnergy
	}
	t.Logf("Test band after normalize: %v", testBand)
	t.Logf("Signs preserved: %s, %s, %s, %s",
		signStr(1.0), signStr(testBand[0]),
		signStr(-3.0), signStr(testBand[2]))

	// Step 4: Check what PVQ does to signs
	t.Log("\n=== PVQ Sign Analysis ===")
	t.Log("PVQ encodes unit-norm vectors with integer pulses.")
	t.Log("The sign of each coefficient is encoded explicitly.")
	t.Log("If there's a sign flip, it's likely in:")
	t.Log("  1. MDCT computation (unlikely - already verified)")
	t.Log("  2. Pre-emphasis filter (let's check)")
	t.Log("  3. De-emphasis in decoder (we use libopus decoder)")
	t.Log("  4. Some other encoding stage")

	// Check if pre-emphasis inverts sign
	t.Log("\n=== Pre-emphasis Sign Check ===")
	testInput := []float64{0.5, 0.4, 0.3, 0.2, 0.1}
	enc2 := celt.NewEncoder(1)
	testPreemph := enc2.ApplyPreemphasis(testInput)
	t.Logf("Input:      %v", testInput)
	t.Logf("Pre-emph:   %v", testPreemph)
	allPositive := true
	for _, v := range testPreemph {
		if v < 0 {
			allPositive = false
			break
		}
	}
	t.Logf("All positive input -> all positive pre-emph: %v", allPositive)
}

func signStr(v float64) string {
	if v >= 0 {
		return "+"
	}
	return "-"
}
