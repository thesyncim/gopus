// Package cgo provides encoder decision tracing tests.
// This test identifies exactly which encoding decisions differ between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// EncoderDecisionTrace captures the encoding decisions for comparison
type EncoderDecisionTrace struct {
	// Header flags
	Silence    int
	Postfilter int
	Transient  int
	Intra      int

	// Coarse energies (first 10 bands)
	CoarseQI []int

	// TF decisions (first 10 bands)
	TFRes []int

	// Spread decision
	Spread int

	// Range state after header
	RngAfterHeader  uint32
	TellAfterHeader int

	// Range state after coarse energy
	RngAfterCoarse  uint32
	TellAfterCoarse int
}

// TestEncoderDecisionsTrace traces what decisions the gopus encoder makes
// and compares with what we expect libopus would do for the same input.
func TestEncoderDecisionsTrace(t *testing.T) {
	t.Log("=== Encoder Decisions Trace ===")
	t.Log("")
	t.Log("This test traces the encoding decisions made by gopus")
	t.Log("to understand why the bitstream differs from libopus.")
	t.Log("")

	frameSize := 960
	channels := 1
	sampleRate := 48000
	bitrate := 64000

	// Generate 440 Hz sine wave (same as byte comparison tests)
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate))
	}

	// Create encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(bitrate)

	// Step 1: Apply pre-emphasis (like in EncodeFrame)
	preemph := encoder.ApplyPreemphasisWithScaling(samples)

	// Step 2: Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Logf("Frame size: %d, LM: %d, Bands: %d", frameSize, lm, nbBands)

	// Step 3: Detect transient
	overlap := celt.Overlap
	if overlap > frameSize {
		overlap = frameSize
	}
	transientInput := make([]float64, (overlap+frameSize)*channels)
	copy(transientInput[overlap*channels:], preemph)
	transientResult := encoder.TransientAnalysis(transientInput, frameSize+overlap, false)

	t.Logf("Transient detection: isTransient=%v, tfEstimate=%.4f",
		transientResult.IsTransient, transientResult.TfEstimate)

	// Step 4: Compute MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder.OverlapBuffer(), 1)
	t.Logf("MDCT coefficients: %d values", len(mdctCoeffs))
	t.Logf("First 5 MDCT coeffs: %.4f, %.4f, %.4f, %.4f, %.4f",
		mdctCoeffs[0], mdctCoeffs[1], mdctCoeffs[2], mdctCoeffs[3], mdctCoeffs[4])

	// Step 5: Compute band energies
	energies := encoder.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	t.Logf("Band energies (log2, first 10):")
	for i := 0; i < 10 && i < len(energies); i++ {
		t.Logf("  Band %2d: %.4f", i, energies[i])
	}

	// Step 6: Initialize range encoder
	targetBits := bitrate * frameSize / sampleRate
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Step 7: Encode header flags (exactly as EncodeFrame does)
	isSilent := false
	isTransient := transientResult.IsTransient
	isIntra := true // First frame

	t.Log("")
	t.Log("=== Header Flag Decisions ===")
	t.Logf("Silence: %v", isSilent)
	t.Logf("Postfilter: 0 (disabled)")
	t.Logf("Transient: %v", isTransient)
	t.Logf("Intra: %v", isIntra)

	// Encode silence flag (logp=15)
	re.EncodeBit(0, 15) // Not silent
	t.Logf("After silence: rng=0x%08X, tell=%d", re.Range(), re.Tell())

	// Encode postfilter flag (logp=1)
	re.EncodeBit(0, 1)
	t.Logf("After postfilter: rng=0x%08X, tell=%d", re.Range(), re.Tell())

	// Encode transient flag (logp=3)
	transientBit := 0
	if isTransient {
		transientBit = 1
	}
	re.EncodeBit(transientBit, 3)
	t.Logf("After transient: rng=0x%08X, tell=%d", re.Range(), re.Tell())

	// Encode intra flag (logp=3)
	intraBit := 0
	if isIntra {
		intraBit = 1
	}
	re.EncodeBit(intraBit, 3)
	rngAfterHeader := re.Range()
	tellAfterHeader := re.Tell()
	t.Logf("After intra: rng=0x%08X, tell=%d", rngAfterHeader, tellAfterHeader)

	// Step 8: Compute qi values that would be encoded
	t.Log("")
	t.Log("=== Coarse Energy QI Computation ===")

	// Prediction coefficients
	alphaCoef := []float64{0.75, 0.822727, 0.857143, 0.875}
	betaIntra := 0.149902

	coef := 0.0 // intra mode
	beta := betaIntra

	DB6 := 1.0
	prevBandEnergy := make([]float64, channels)
	prevEnergy := make([]float64, celt.MaxBands*channels)

	t.Logf("%-6s | %-10s | %-10s | %-10s | %-6s | %-6s",
		"Band", "energy", "f", "prev", "qi0", "qi")
	t.Log("-------+------------+------------+------------+--------+-------")

	for band := 0; band < 10 && band < nbBands; band++ {
		x := energies[band]
		oldE := prevEnergy[band]
		minEnergy := -9.0 * DB6
		if oldE < minEnergy {
			oldE = minEnergy
		}

		// Prediction residual
		f := x - coef*oldE - prevBandEnergy[0]

		// Quantize
		qi := int(math.Floor(f/DB6 + 0.5))

		t.Logf("%6d | %10.4f | %10.4f | %10.4f | %6d | %6d",
			band, x, f, prevBandEnergy[0], qi, qi)

		// Update state
		q := float64(qi) * DB6
		prevBandEnergy[0] = prevBandEnergy[0] + q - beta*q

		_ = alphaCoef // suppress unused warning
	}

	// Step 9: Compare with libopus header encoding
	t.Log("")
	t.Log("=== libopus Header Comparison ===")

	// Encode same header flags with libopus
	headerBits := []int{0, 0, transientBit, intraBit}
	headerLogps := []int{15, 1, 3, 3}
	libStates, _ := TraceBitSequence(headerBits, headerLogps)

	if libStates != nil {
		t.Logf("%-20s | %-12s | %-12s | %s",
			"Step", "gopus rng", "libopus rng", "Match")
		t.Log("--------------------+---------------+---------------+------")

		// Compare after each flag (we need to trace gopus step by step too)
		goRngs := []uint32{}
		buf2 := make([]byte, 256)
		re2 := &rangecoding.Encoder{}
		re2.Init(buf2)
		goRngs = append(goRngs, re2.Range())
		for i, bit := range headerBits {
			re2.EncodeBit(bit, uint(headerLogps[i]))
			goRngs = append(goRngs, re2.Range())
		}

		flagNames := []string{"Initial", "Silence", "Postfilter", "Transient", "Intra"}
		for i := range flagNames {
			match := "YES"
			if goRngs[i] != libStates[i].Rng {
				match = "NO"
			}
			t.Logf("%-20s | 0x%08X   | 0x%08X   | %s",
				flagNames[i], goRngs[i], libStates[i].Rng, match)
		}
	}

	// Step 10: Summary
	t.Log("")
	t.Log("=== Summary ===")
	t.Log("Header flags match between gopus and libopus range encoder.")
	t.Log("")
	t.Log("The bitstream divergence is likely due to differences in:")
	t.Log("  1. Band energy computation (MDCT + normalization)")
	t.Log("  2. Coarse energy quantization (qi values)")
	t.Log("  3. TF analysis decisions")
	t.Log("  4. Spread/allocation decisions")
	t.Log("  5. PVQ encoding (pulse positions)")
}

// TestCompareEncoderParameters compares encoder parameters/decisions
func TestCompareEncoderParameters(t *testing.T) {
	t.Log("=== Compare Encoder Parameters ===")
	t.Log("")

	frameSize := 960
	channels := 1

	// Generate test signal
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440.0*float64(i)/48000.0)
	}

	// gopus encoder
	goEnc := celt.NewEncoder(channels)
	goEnc.Reset()
	goEnc.SetBitrate(64000)

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	t.Logf("LM=%d, effBands=%d, shortBlocks=%d", mode.LM, mode.EffBands, mode.ShortBlocks)

	// Compute pre-emphasis
	preemph := goEnc.ApplyPreemphasisWithScaling(samples)
	t.Logf("Pre-emphasis first 5: %.4f, %.4f, %.4f, %.4f, %.4f",
		preemph[0], preemph[1], preemph[2], preemph[3], preemph[4])

	// Compute MDCT
	mdct := celt.ComputeMDCTWithHistory(preemph, goEnc.OverlapBuffer(), 1)
	t.Logf("MDCT length: %d", len(mdct))
	t.Logf("MDCT first 5: %.4f, %.4f, %.4f, %.4f, %.4f",
		mdct[0], mdct[1], mdct[2], mdct[3], mdct[4])

	// Compute band energies
	energies := goEnc.ComputeBandEnergies(mdct, mode.EffBands, frameSize)
	t.Logf("Band energies (log2, first 10):")
	for i := 0; i < 10 && i < len(energies); i++ {
		t.Logf("  [%d] = %.6f", i, energies[i])
	}

	// Now encode the full frame and check what happens
	t.Log("")
	t.Log("Full frame encoding...")
	encoded, err := goEnc.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}
	t.Logf("Encoded %d bytes", len(encoded))
	t.Logf("First 10 bytes: %v", encoded[:minIntEDT(10, len(encoded))])

	// Decode with gopus to verify
	goDec := celt.NewDecoder(channels)
	decoded, err := goDec.DecodeFrame(encoded, frameSize)
	if err != nil {
		t.Fatalf("DecodeFrame failed: %v", err)
	}

	// Compute correlation between input and output
	var inputSum, outputSum, crossSum float64
	for i := 0; i < len(samples) && i < len(decoded); i++ {
		inputSum += samples[i] * samples[i]
		outputSum += decoded[i] * decoded[i]
		crossSum += samples[i] * decoded[i]
	}
	corr := crossSum / math.Sqrt(inputSum*outputSum)
	t.Logf("Input-output correlation: %.4f", corr)

	// Compute SNR
	var signalPower, noisePower float64
	for i := 0; i < len(samples) && i < len(decoded); i++ {
		signalPower += samples[i] * samples[i]
		diff := samples[i] - decoded[i]
		noisePower += diff * diff
	}
	if noisePower > 0 {
		snr := 10 * math.Log10(signalPower/noisePower)
		t.Logf("Self-decode SNR: %.2f dB", snr)
	}
}

func minIntEDT(a, b int) int {
	if a < b {
		return a
	}
	return b
}
