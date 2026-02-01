//go:build trace
// +build trace

// Package cgo extracts QI values from actual encoder outputs.
// Agent 23: Extract and compare coarse energy QI values.
package cgo

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/celt"
)

// TestExtractQIValues extracts QI values from both encoders by decoding packets
func TestExtractQIValues(t *testing.T) {
	t.Log("=== Agent 23: QI Value Extraction ===")
	t.Log("")

	// Generate 440Hz sine wave test signal
	frameSize := 960
	channels := 1
	freq := 440.0
	amp := 0.5

	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		sample := amp * math.Sin(2.0*math.Pi*freq*float64(i)/48000.0)
		pcm[i] = float32(sample)
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)
	libEnc.SetSignal(OpusSignalMusic)

	libBytes, libLen := libEnc.EncodeFloat(pcm, frameSize)
	if libLen < 0 {
		t.Fatalf("libopus encode failed: %d", libLen)
	}

	// Encode with gopus
	gopusEnc, err := gopus.NewEncoder(48000, channels, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("Failed to create gopus encoder: %v", err)
	}
	_ = gopusEnc.SetBitrate(64000)
	gopusEnc.SetFrameSize(frameSize)

	gopusPacket := make([]byte, 4000)
	gopusLen, err := gopusEnc.Encode(pcm, gopusPacket)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	t.Logf("libopus: %d bytes", libLen)
	t.Logf("gopus: %d bytes", gopusLen)
	t.Logf("")

	// Decode libopus packet to extract decoded values
	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("Failed to create libopus decoder: %v", err)
	}
	defer libDec.Destroy()

	libDecoded, libSamples := libDec.DecodeFloat(libBytes[:libLen], frameSize)
	t.Logf("libopus decoded: %d samples", libSamples)

	// Decode gopus packet with libopus decoder to see if it's valid
	gopusDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("Failed to create libopus decoder for gopus: %v", err)
	}
	defer gopusDec.Destroy()

	gopusDecoded, gopusSamples := gopusDec.DecodeFloat(gopusPacket[:gopusLen], frameSize)
	t.Logf("gopus decoded by libopus: %d samples", gopusSamples)

	// Compute correlation between decoded outputs
	if libSamples > 0 && gopusSamples > 0 {
		minSamples := libSamples
		if gopusSamples < minSamples {
			minSamples = gopusSamples
		}

		var dotProd, libNorm, gopusNorm float64
		for i := 0; i < minSamples; i++ {
			dotProd += float64(libDecoded[i]) * float64(gopusDecoded[i])
			libNorm += float64(libDecoded[i]) * float64(libDecoded[i])
			gopusNorm += float64(gopusDecoded[i]) * float64(gopusDecoded[i])
		}

		if libNorm > 0 && gopusNorm > 0 {
			corr := dotProd / (math.Sqrt(libNorm) * math.Sqrt(gopusNorm))
			t.Logf("Decoded correlation: %.6f", corr)
		}

		// Compute error
		var mse float64
		for i := 0; i < minSamples; i++ {
			diff := float64(libDecoded[i]) - float64(gopusDecoded[i])
			mse += diff * diff
		}
		mse /= float64(minSamples)
		rmse := math.Sqrt(mse)
		t.Logf("RMSE between decoded outputs: %.6f", rmse)
	}

	// Now trace what gopus internal encoder produces for qi values
	t.Log("")
	t.Log("=== Gopus Internal QI Values ===")
	traceGopusQIValues(t, pcm, frameSize, channels)
}

// traceGopusQIValues traces the qi values gopus would encode
func traceGopusQIValues(t *testing.T, pcm []float32, frameSize, channels int) {
	// Convert to float64 for internal encoder
	pcm64 := make([]float64, len(pcm))
	for i, v := range pcm {
		pcm64[i] = float64(v)
	}

	// Create encoder
	enc := celt.NewEncoder(channels)
	enc.Reset()
	enc.SetBitrate(64000)

	// Apply pre-emphasis
	preemph := enc.ApplyPreemphasisWithScaling(pcm64)

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Run transient detection
	overlap := celt.Overlap
	transientInput := make([]float64, overlap+frameSize)
	copy(transientInput[overlap:], preemph)
	result := enc.TransientAnalysis(transientInput, frameSize+overlap, false)

	// Force transient for frame 0 (matches libopus)
	transient := result.IsTransient
	if enc.FrameCount() == 0 && lm > 0 {
		transient = true
	}

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	t.Logf("transient=%v, shortBlocks=%d", transient, shortBlocks)

	// MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)

	// Band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Compute QI values using the same algorithm as EncodeCoarseEnergy
	// For inter mode (intra=false)
	DB6 := 1.0
	coef := 16384.0 / 32768.0 // AlphaCoef[3] for lm=3
	beta := 6554.0 / 32768.0  // BetaCoefInter[3] for lm=3

	qiValues := make([]int, nbBands)
	prevBandEnergy := 0.0

	t.Log("")
	t.Logf("Band | Energy   | Predicted | Residual | QI")
	t.Logf("-----+----------+-----------+----------+----")

	for band := 0; band < nbBands; band++ {
		x := energies[band]

		// Previous frame energy = 0 (first frame)
		oldE := 0.0

		// Prediction residual
		predicted := coef*oldE + prevBandEnergy
		f := x - predicted

		// Quantize
		qi := int(math.Floor(f/DB6 + 0.5))
		qiValues[band] = qi

		if band < 15 { // Show first 15 bands
			t.Logf("%4d | %8.4f | %9.4f | %8.4f | %3d",
				band, x, predicted, f, qi)
		}

		// Update state
		q := float64(qi) * DB6
		prevBandEnergy = prevBandEnergy + q - beta*q
	}

	// Show all qi values
	t.Log("")
	t.Log("All QI values:")
	qiStr := ""
	for i, qi := range qiValues {
		if i > 0 {
			qiStr += ", "
		}
		qiStr += fmt.Sprintf("%d", qi)
	}
	t.Logf("[%s]", qiStr)

	// Now compare with what libopus would produce for the same qi values
	t.Log("")
	t.Log("=== Encoding QI values through both encoders ===")

	// Get probability model
	probModel := celt.GetEProbModel()
	prob := probModel[lm][0] // inter mode

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

	// Header flags matching what gopus uses
	headerBits := []int{0, 0, 1, 0} // silence=0, postfilter=0, transient=1, intra=0
	headerLogps := []int{15, 1, 3, 3}

	// Encode with libopus
	_, libQIs, libBytes := TraceHeaderPlusLaplace(
		headerBits, headerLogps, qiValues, fsValues, decayValues)

	t.Logf("libopus encoding of same QI values: %02x...", libBytes[:minQI(10, len(libBytes))])

	// Check if any QI values were modified by Laplace encoding
	modified := false
	for i := 0; i < nbBands; i++ {
		if qiValues[i] != libQIs[i] {
			t.Logf("Band %d: input qi=%d, encoded qi=%d (MODIFIED)", i, qiValues[i], libQIs[i])
			modified = true
		}
	}
	if !modified {
		t.Log("All QI values preserved by Laplace encoding")
	}

	// Now the key comparison: what does the ACTUAL gopus encoder produce?
	t.Log("")
	t.Log("=== Actual gopus encoder output ===")

	// Run the full encoder
	encoded, err := enc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Logf("gopus encode failed: %v", err)
		return
	}

	t.Logf("gopus actual output: %02x...", encoded[:minQI(10, len(encoded))])
	t.Logf("libopus from QI: %02x...", libBytes[:minQI(10, len(libBytes))])

	// Compare
	matching := 0
	firstDiff := -1
	for i := 0; i < len(encoded) && i < len(libBytes); i++ {
		if encoded[i] == libBytes[i] {
			matching++
		} else if firstDiff < 0 {
			firstDiff = i
		}
	}

	t.Logf("")
	t.Logf("Matching bytes: %d, first difference at byte %d", matching, firstDiff)

	if firstDiff >= 0 {
		t.Logf("At byte %d: gopus=0x%02X, libopus=0x%02X", firstDiff, encoded[firstDiff], libBytes[firstDiff])
	}
}

func minQI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
