//go:build trace
// +build trace

// Package cgo traces the root cause of byte 7 divergence.
// Agent 23: Extract gopus QI values and re-encode through libopus Laplace.
package cgo

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestByte7RootCause identifies root cause of byte 7 divergence
func TestByte7RootCause(t *testing.T) {
	t.Log("=== Agent 23: Byte 7 Root Cause Analysis ===")
	t.Log("")

	frameSize := 960
	channels := 1
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize*channels)
	pcm64 := make([]float64, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		sample := 0.5 * math.Sin(2.0*math.Pi*440*float64(i)/48000.0)
		pcm32[i] = float32(sample)
		pcm64[i] = sample
	}

	// Create CELT encoder and process signal exactly as EncodeFrame does
	enc := celt.NewEncoder(channels)
	enc.Reset()
	enc.SetBitrate(bitrate)

	// Step 1: Apply DC rejection (matches EncodeFrame line 64)
	dcRejected := enc.ApplyDCReject(pcm64)

	// Step 2: Delay buffer handling (matches EncodeFrame lines 66-87)
	// For first frame, delay buffer is zeros, so we take first frameSize samples
	expectedLen := frameSize * channels
	delayComp := celt.DelayCompensation * channels
	combinedLen := delayComp + len(dcRejected)
	combinedBuf := make([]float64, combinedLen)
	// delay buffer is zeros for first frame
	copy(combinedBuf[delayComp:], dcRejected)
	samplesForFrame := combinedBuf[:expectedLen]

	// Step 3: Pre-emphasis (matches EncodeFrame line 94)
	preemph := enc.ApplyPreemphasisWithScaling(samplesForFrame)

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	// Step 4: Transient detection (simplified for first frame - force transient)
	transient := true // First frame forces transient for lm > 0

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Step 5: MDCT with overlap
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)

	// Step 6: Compute band energies
	energies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Logf("Mode: lm=%d, nbBands=%d, shortBlocks=%d", lm, nbBands, shortBlocks)
	t.Log("")

	// Set up range encoder (matching EncodeFrame setup)
	targetBits := bitrate * frameSize / 48000
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	// Connect encoder to range encoder
	enc.SetRangeEncoder(re)
	enc.SetFrameBitsForTest(targetBits)

	// Encode header flags (matching EncodeFrame lines 298-348)
	re.EncodeBit(0, 15)           // silence=0
	re.EncodeBit(0, 1)            // postfilter=0
	re.EncodeBit(1, 3)            // transient=1 (forced for first frame)
	re.EncodeBit(0, 3)            // intra=0 (inter mode)

	tellAfterHeader := re.Tell()
	t.Logf("After header: tell=%d bits", tellAfterHeader)

	// Now encode coarse energy
	intra := false
	quantizedEnergies := enc.EncodeCoarseEnergy(energies, nbBands, intra, lm)

	tellAfterCoarse := re.Tell()
	t.Logf("After coarse energy: tell=%d bits", tellAfterCoarse)

	// Get bytes
	gopusReconBytes := re.Done()
	t.Logf("Gopus reconstruction (header+coarse): %02x", gopusReconBytes[:minRC(16, len(gopusReconBytes))])
	t.Log("")

	// Extract QI values from quantized energies
	qiValues := make([]int, nbBands)
	DB6 := 1.0
	beta := celt.BetaCoefInter[lm]
	prevBandE := 0.0

	t.Log("=== Extracted QI values ===")
	t.Log("Band | Energy    | Quantized | QI")
	t.Log("-----+-----------+-----------+----")
	for band := 0; band < nbBands; band++ {
		q := quantizedEnergies[band]
		qiEstimate := (q - prevBandE) / DB6
		qi := int(math.Round(qiEstimate))
		qiValues[band] = qi

		if band < 21 {
			t.Logf("%4d | %9.4f | %9.4f | %3d", band, energies[band], q, qi)
		}

		qVal := float64(qi) * DB6
		prevBandE = prevBandE + qVal - beta*qVal
	}

	// Now encode the SAME QI values through libopus Laplace
	t.Log("")
	t.Log("=== Encoding QI values through libopus ===")

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

	headerBits := []int{0, 0, 1, 0}    // silence=0, postfilter=0, transient=1, intra=0
	headerLogps := []int{15, 1, 3, 3}

	_, libQIs, libBytes := TraceHeaderPlusLaplace(
		headerBits, headerLogps, qiValues, fsValues, decayValues)

	t.Logf("libopus from same QI: %02x", libBytes[:minRC(16, len(libBytes))])
	t.Log("")

	// Check if QI values were modified
	modified := false
	for i := 0; i < nbBands; i++ {
		if qiValues[i] != libQIs[i] {
			t.Logf("QI modified: band %d, input=%d, output=%d", i, qiValues[i], libQIs[i])
			modified = true
		}
	}
	if !modified {
		t.Log("All QI values preserved by Laplace encoding")
	}

	// Compare bytes
	t.Log("")
	t.Log("=== Byte-by-byte comparison ===")
	t.Logf("gopus:   %02x", gopusReconBytes[:minRC(16, len(gopusReconBytes))])
	t.Logf("libopus: %02x", libBytes[:minRC(16, len(libBytes))])

	matching := 0
	firstDiff := -1
	for i := 0; i < minRC(len(gopusReconBytes), len(libBytes)); i++ {
		if gopusReconBytes[i] == libBytes[i] {
			matching++
		} else if firstDiff < 0 {
			firstDiff = i
		}
	}
	t.Logf("Matching bytes: %d, first diff at byte %d", matching, firstDiff)

	if firstDiff >= 0 && firstDiff < len(gopusReconBytes) && firstDiff < len(libBytes) {
		t.Logf("At byte %d: gopus=0x%02X (%08b), libopus=0x%02X (%08b)",
			firstDiff, gopusReconBytes[firstDiff], gopusReconBytes[firstDiff],
			libBytes[firstDiff], libBytes[firstDiff])
	}

	// Now compare with actual full encoder output
	t.Log("")
	t.Log("=== Comparison with actual gopus encoder ===")

	// Encode through full pipeline
	enc2 := celt.NewEncoder(channels)
	enc2.Reset()
	enc2.SetBitrate(bitrate)

	fullOutput, _ := enc2.EncodeFrame(pcm64, frameSize)
	t.Logf("Full encoder output: %02x", fullOutput[:minRC(16, len(fullOutput))])

	// Compare reconstruction with full output
	reconMatch := true
	for i := 0; i < minRC(len(gopusReconBytes), len(fullOutput)); i++ {
		if gopusReconBytes[i] != fullOutput[i] {
			reconMatch = false
			t.Logf("Reconstruction vs Full: First diff at byte %d (recon=0x%02X, full=0x%02X)",
				i, gopusReconBytes[i], fullOutput[i])
			break
		}
	}
	if reconMatch {
		t.Log("Reconstruction matches full encoder (header+coarse portion)")
	}

	// And compare with libopus full encoder
	t.Log("")
	t.Log("=== Comparison with libopus full encoder ===")

	libEnc, _ := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if libEnc != nil {
		defer libEnc.Destroy()
		libEnc.SetBitrate(bitrate)
		libEnc.SetComplexity(10)
		libEnc.SetBandwidth(OpusBandwidthFullband)
		libEnc.SetVBR(true)
		libEnc.SetSignal(OpusSignalMusic)

		libFullBytes, libFullLen := libEnc.EncodeFloat(pcm32, frameSize)
		if libFullLen > 0 {
			libPayload := libFullBytes[1:] // Skip TOC
			t.Logf("libopus full encoder payload: %02x", libPayload[:minRC(16, len(libPayload))])

			// Compare reconstruction with libopus payload
			reconLibMatch := 0
			firstLibDiff := -1
			for i := 0; i < minRC(len(gopusReconBytes), len(libPayload)); i++ {
				if gopusReconBytes[i] == libPayload[i] {
					reconLibMatch++
				} else if firstLibDiff < 0 {
					firstLibDiff = i
				}
			}
			t.Logf("Reconstruction vs libopus payload: %d matching, first diff at byte %d",
				reconLibMatch, firstLibDiff)

			if firstLibDiff >= 0 {
				t.Logf("At diff: recon=0x%02X, libopus=0x%02X",
					gopusReconBytes[firstLibDiff], libPayload[firstLibDiff])
			}
		}
	}

	// Print QI summary
	t.Log("")
	t.Log("=== QI Summary ===")
	qiStr := ""
	for i, qi := range qiValues {
		if i > 0 {
			qiStr += ", "
		}
		qiStr += fmt.Sprintf("%d", qi)
	}
	t.Logf("QI values: [%s]", qiStr)
}

func minRC(a, b int) int {
	if a < b {
		return a
	}
	return b
}
