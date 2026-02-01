//go:build trace
// +build trace

// Package cgo provides precise QI value comparison.
// Agent 23: Match exact QI computation with decay bounds and bit budget.
package cgo

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestPreciseQIComputation tests QI computation with all edge cases.
func TestPreciseQIComputation(t *testing.T) {
	t.Log("=== Agent 23: Precise QI Computation ===")
	t.Log("")

	frameSize := 960
	channels := 1
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*440*float64(i)/48000.0)
	}

	// Create encoder
	enc := celt.NewEncoder(channels)
	enc.Reset()
	enc.SetBitrate(bitrate)

	// Apply pre-emphasis
	preemph := enc.ApplyPreemphasisWithScaling(pcm)

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Run transient detection
	overlap := celt.Overlap
	transientInput := make([]float64, overlap+frameSize)
	copy(transientInput[overlap:], preemph)
	result := enc.TransientAnalysis(transientInput, frameSize+overlap, false)

	// Force transient for frame 0
	transient := result.IsTransient
	if enc.FrameCount() == 0 && lm > 0 {
		transient = true
	}

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	t.Logf("transient=%v, shortBlocks=%d, lm=%d", transient, shortBlocks, lm)

	// MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)

	// Band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	// Set up range encoder (matching EncodeFrame setup)
	targetBits := bitrate * frameSize / 48000
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Encode header flags first
	re.EncodeBit(0, 15) // silence=0
	re.EncodeBit(0, 1)  // postfilter=0
	transientBit := 0
	if transient {
		transientBit = 1
	}
	re.EncodeBit(transientBit, 3) // transient
	re.EncodeBit(0, 3)            // intra=0 (inter mode)

	t.Logf("After header: tell=%d bits, budget=%d bits", re.Tell(), targetBits)
	t.Log("")

	// Now encode coarse energy using the encoder
	enc.SetRangeEncoder(re)
	enc.SetFrameBitsForTest(targetBits)

	// Get quantized energies from EncodeCoarseEnergy
	intra := false
	quantizedEnergies := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	tellAfterCoarse := re.Tell()
	t.Logf("After coarse energy: tell=%d bits", tellAfterCoarse)

	// Show the QI values that were actually encoded
	// We can infer them from the quantized energies
	t.Log("")
	t.Log("=== Encoded Values ===")
	t.Log("Band | Input     | Quantized | Diff (QI estimate)")
	t.Log("-----+-----------+-----------+-------------------")

	DB6 := 1.0
	_ = celt.AlphaCoef[lm] // Used in EncodeCoarseEnergy
	beta := celt.BetaCoefInter[lm]

	prevBandE := 0.0
	for band := 0; band < 15 && band < nbBands; band++ {
		x := energies[band]
		q := quantizedEnergies[band]

		// Compute what qi was used (reverse the quantization)
		// q = coef*oldE + prevBandE + qi*DB6
		// For first frame, oldE = 0
		qiEstimate := (q - prevBandE) / DB6

		t.Logf("%4d | %9.4f | %9.4f | %6.2f (qi~%d)",
			band, x, q, q-prevBandE, int(math.Round(qiEstimate)))

		// Update prevBandE (same logic as encoder)
		qi := int(math.Round(qiEstimate))
		qVal := float64(qi) * DB6
		prevBandE = prevBandE + qVal - beta*qVal
	}

	// Now encode the same QI values through libopus to compare
	t.Log("")
	t.Log("=== Comparison with libopus encoding ===")

	// Extract the actual QI values from the encoder output
	actualQI := make([]int, nbBands)
	prevBandE = 0.0
	for band := 0; band < nbBands; band++ {
		q := quantizedEnergies[band]
		qiEstimate := (q - prevBandE) / DB6
		actualQI[band] = int(math.Round(qiEstimate))

		qi := actualQI[band]
		qVal := float64(qi) * DB6
		prevBandE = prevBandE + qVal - beta*qVal
	}

	t.Logf("Actual QI values from gopus encoder:")
	qiStr := ""
	for i, qi := range actualQI {
		if i > 0 {
			qiStr += ", "
		}
		qiStr += fmt.Sprintf("%d", qi)
	}
	t.Logf("[%s]", qiStr)

	// Encode through libopus
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

	headerBits := []int{0, 0, transientBit, 0}
	headerLogps := []int{15, 1, 3, 3}

	_, libQIs, libBytes := TraceHeaderPlusLaplace(
		headerBits, headerLogps, actualQI, fsValues, decayValues)

	t.Logf("libopus encoding: %02x...", libBytes[:minPQ(10, len(libBytes))])

	t.Logf("")
	// Done() finalizes the encoder and returns bytes
	gopusBytes := re.Done()

	// Compare bytes
	matching := 0
	firstDiff := -1
	for i := 0; i < len(gopusBytes) && i < len(libBytes); i++ {
		if gopusBytes[i] == libBytes[i] {
			matching++
		} else if firstDiff < 0 {
			firstDiff = i
		}
	}

	t.Logf("Byte comparison:")
	t.Logf("  gopus: %02x...", gopusBytes[:minPQ(10, len(gopusBytes))])
	t.Logf("  libopus: %02x...", libBytes[:minPQ(10, len(libBytes))])

	// Re-compare after getting bytes
	matching = 0
	firstDiff = -1
	for i := 0; i < len(gopusBytes) && i < len(libBytes); i++ {
		if gopusBytes[i] == libBytes[i] {
			matching++
		} else if firstDiff < 0 {
			firstDiff = i
		}
	}
	t.Logf("  Matching: %d bytes, first diff at byte %d", matching, firstDiff)

	// Check if QI values were modified by libopus
	modified := false
	for i := 0; i < nbBands && i < len(libQIs); i++ {
		if actualQI[i] != libQIs[i] {
			t.Logf("  Band %d: input qi=%d, encoded qi=%d (MODIFIED)", i, actualQI[i], libQIs[i])
			modified = true
		}
	}
	if !modified {
		t.Log("  All QI values preserved by Laplace encoding")
	}

	// If there's a difference, show the exact bytes
	if firstDiff >= 0 && firstDiff < len(gopusBytes) && firstDiff < len(libBytes) {
		t.Logf("")
		t.Logf("Divergence details at byte %d:", firstDiff)
		t.Logf("  gopus: 0x%02X (%08b)", gopusBytes[firstDiff], gopusBytes[firstDiff])
		t.Logf("  libopus: 0x%02X (%08b)", libBytes[firstDiff], libBytes[firstDiff])
	}
}

func minPQ(a, b int) int {
	if a < b {
		return a
	}
	return b
}
