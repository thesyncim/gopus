// Package cgo extracts actual QI values from full encoder by comparing state.
// Agent 23: Extract actual QI values produced by EncodeFrame.
package cgo

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestExtractActualQI extracts QI values from full encoder
func TestExtractActualQI(t *testing.T) {
	t.Log("=== Agent 23: Extract Actual QI from Full Encoder ===")
	t.Log("")

	frameSize := 960
	channels := 1
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize*channels)
	pcm32 := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		sample := 0.5 * math.Sin(2.0*math.Pi*440*float64(i)/48000.0)
		pcm64[i] = sample
		pcm32[i] = float32(sample)
	}

	// Create encoder and encode
	enc := celt.NewEncoder(channels)
	enc.Reset()
	enc.SetBitrate(bitrate)

	// Get prevEnergy before encoding (should be zeros)
	prevBefore := enc.PrevEnergy()
	t.Logf("PrevEnergy before encoding (first 5): %v", prevBefore[:minEQ(5, len(prevBefore))])

	// Encode
	output, err := enc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	t.Logf("Encoded %d bytes: %02x", len(output), output[:minEQ(16, len(output))])

	// Get prevEnergy after encoding - this IS the quantized energy
	prevAfter := enc.PrevEnergy()
	t.Log("")
	t.Log("=== Quantized energies (from prevEnergy after encoding) ===")
	t.Log("Band | Quantized")
	t.Log("-----+----------")
	for i := 0; i < minEQ(21, len(prevAfter)); i++ {
		t.Logf("%4d | %9.4f", i, prevAfter[i])
	}

	// Compute QI values from quantized energies
	t.Log("")
	t.Log("=== Computed QI values ===")

	mode := celt.GetModeConfig(frameSize)
	lm := mode.LM
	beta := celt.BetaCoefInter[lm]
	DB6 := 1.0

	qiValues := make([]int, len(prevAfter))
	prevBandE := 0.0

	t.Log("Band | Quantized | PrevBandE | Diff     | QI")
	t.Log("-----+-----------+-----------+----------+----")
	for band := 0; band < minEQ(21, len(prevAfter)); band++ {
		q := prevAfter[band]
		diff := q - prevBandE
		qi := int(math.Round(diff / DB6))
		qiValues[band] = qi

		t.Logf("%4d | %9.4f | %9.4f | %8.4f | %3d", band, q, prevBandE, diff, qi)

		qVal := float64(qi) * DB6
		prevBandE = prevBandE + qVal - beta*qVal
	}

	// Print QI summary
	t.Log("")
	qiStr := ""
	for i := 0; i < minEQ(21, len(qiValues)); i++ {
		if i > 0 {
			qiStr += ", "
		}
		qiStr += fmt.Sprintf("%d", qiValues[i])
	}
	t.Logf("Actual QI values from full encoder: [%s]", qiStr)

	// Now encode the same QI values through libopus to compare
	t.Log("")
	t.Log("=== Encoding actual QI through libopus ===")

	probModel := celt.GetEProbModel()
	prob := probModel[lm][0] // inter mode (intra=false)

	nbBands := len(qiValues)
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

	// Header: silence=0, postfilter=0, transient=1, intra=0
	headerBits := []int{0, 0, 1, 0}
	headerLogps := []int{15, 1, 3, 3}

	_, libQIs, libBytes := TraceHeaderPlusLaplace(
		headerBits, headerLogps, qiValues, fsValues, decayValues)

	t.Logf("libopus from actual QI: %02x", libBytes[:minEQ(16, len(libBytes))])
	t.Logf("gopus full encoder:     %02x", output[:minEQ(16, len(output))])

	// Compare
	matching := 0
	firstDiff := -1
	for i := 0; i < minEQ(len(libBytes), len(output)); i++ {
		if libBytes[i] == output[i] {
			matching++
		} else if firstDiff < 0 {
			firstDiff = i
		}
	}
	t.Logf("Matching bytes: %d, first diff at byte %d", matching, firstDiff)

	if firstDiff >= 0 {
		t.Logf("At diff: libopus=0x%02X, gopus=0x%02X", libBytes[firstDiff], output[firstDiff])
	}

	// Check if QI values were modified
	modified := false
	for i := 0; i < minEQ(len(qiValues), len(libQIs)); i++ {
		if qiValues[i] != libQIs[i] {
			t.Logf("QI modified: band %d, input=%d, output=%d", i, qiValues[i], libQIs[i])
			modified = true
		}
	}
	if !modified {
		t.Log("All QI values preserved by Laplace encoding")
	}

	// Also compare with libopus full encoder
	t.Log("")
	t.Log("=== libopus full encoder comparison ===")

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)
	libEnc.SetSignal(OpusSignalMusic)

	libFullBytes, libFullLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libFullLen < 0 {
		t.Fatalf("libopus encode failed: %d", libFullLen)
	}

	libPayload := libFullBytes[1:] // Skip TOC
	t.Logf("libopus full payload:   %02x", libPayload[:minEQ(16, len(libPayload))])
	t.Logf("gopus full output:      %02x", output[:minEQ(16, len(output))])

	// Compare gopus output with libopus payload
	gopusMatch := 0
	gopusFirstDiff := -1
	for i := 0; i < minEQ(len(output), len(libPayload)); i++ {
		if output[i] == libPayload[i] {
			gopusMatch++
		} else if gopusFirstDiff < 0 {
			gopusFirstDiff = i
		}
	}
	t.Logf("gopus vs libopus: %d matching, first diff at byte %d", gopusMatch, gopusFirstDiff)

	if gopusFirstDiff >= 0 {
		t.Logf("At diff: gopus=0x%02X, libopus=0x%02X", output[gopusFirstDiff], libPayload[gopusFirstDiff])
	}
}

func minEQ(a, b int) int {
	if a < b {
		return a
	}
	return b
}
