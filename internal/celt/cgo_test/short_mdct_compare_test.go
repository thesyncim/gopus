// Package cgo compares short block MDCT between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestShortBlockMDCTComparison compares short block MDCT output between gopus and libopus.
// This is critical because both encoders use transient mode (shortBlocks=8) for the test signal.
func TestShortBlockMDCTComparison(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	overlap := celt.Overlap // 120
	shortBlocks := 8
	shortSize := frameSize / shortBlocks // 120

	// Generate 440Hz sine wave
	pcm := make([]float64, frameSize)
	pcmF32 := make([]float32, frameSize)
	for i := range pcm {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm[i] = val
		pcmF32[i] = float32(val)
	}

	t.Log("=== Short Block MDCT Comparison ===")
	t.Logf("Frame: %d samples, shortBlocks=%d, shortSize=%d, overlap=%d", frameSize, shortBlocks, shortSize, overlap)
	t.Log("")

	// Gopus: Apply pre-emphasis
	enc := celt.NewEncoder(1)
	enc.Reset()
	gopusPreemph := enc.ApplyPreemphasisWithScaling(pcm)

	// Libopus: Apply pre-emphasis
	libPreemph := ApplyLibopusPreemphasis(pcmF32, 0.85)

	// Build input for MDCT: history (zeros for first frame) + pre-emphasized signal
	gopusInput := make([]float64, frameSize+overlap)
	// History is zero for first frame
	copy(gopusInput[overlap:], gopusPreemph)

	libInput := make([]float32, frameSize+overlap)
	for i := 0; i < overlap; i++ {
		libInput[i] = 0 // History zeros
	}
	for i := 0; i < frameSize; i++ {
		libInput[overlap+i] = libPreemph[i]
	}

	t.Log("=== Individual Short Block Comparison ===")
	t.Log("")

	// Get the CELT mode for libopus MDCT
	mode := GetCELTMode48000_960()
	if mode == nil {
		t.Fatal("Failed to create CELT mode")
	}

	// Short MDCT uses shift=3 (nfft=240, n2=120)
	// shift=3 means 1920 >> 3 = 240 = 2 * shortSize
	shift := 3

	// Compare each short block
	for b := 0; b < shortBlocks; b++ {
		t.Logf("=== Block %d ===", b)

		// Extract block input: starts at b*shortSize, length shortSize+overlap
		blockStart := b * shortSize
		blockEnd := blockStart + shortSize + overlap

		// Gopus block input
		gopusBlockInput := gopusInput[blockStart:blockEnd]

		// Libopus block input
		libBlockInput := make([]float32, shortSize+overlap)
		for i := 0; i < shortSize+overlap && blockStart+i < len(libInput); i++ {
			libBlockInput[i] = libInput[blockStart+i]
		}

		// Call libopus MDCT on this block
		libMDCT := mode.MDCTForward(libBlockInput, shift)

		// Gopus: compute MDCT for this block using mdctForwardOverlap
		// (which is what mdctForwardShortOverlap calls)
		gopusMDCT := celt.MDCTForwardWithOverlap(gopusBlockInput, overlap)

		t.Logf("Input samples [%d:%d] (first 5): gopus=%.2f,%.2f,%.2f,%.2f,%.2f  lib=%.4f,%.4f,%.4f,%.4f,%.4f",
			blockStart, blockEnd,
			gopusBlockInput[0], gopusBlockInput[1], gopusBlockInput[2], gopusBlockInput[3], gopusBlockInput[4],
			libBlockInput[0], libBlockInput[1], libBlockInput[2], libBlockInput[3], libBlockInput[4])

		// Compare MDCT outputs
		t.Log("Coeff | Gopus        | Libopus      | Diff")
		t.Log("------+--------------+--------------+---------")

		minLen := len(gopusMDCT)
		if len(libMDCT) < minLen {
			minLen = len(libMDCT)
		}

		maxDiff := 0.0
		maxDiffIdx := 0
		var errPow, sigPow float64

		for i := 0; i < 10 && i < minLen; i++ {
			libVal := float64(libMDCT[i])
			gopusVal := gopusMDCT[i]
			diff := gopusVal - libVal
			absDiff := math.Abs(diff)
			if absDiff > maxDiff {
				maxDiff = absDiff
				maxDiffIdx = i
			}
			errPow += diff * diff
			sigPow += libVal * libVal
			t.Logf("%5d | %12.4f | %12.4f | %8.4f", i, gopusVal, libVal, diff)
		}

		// Compute full SNR
		for i := 10; i < minLen; i++ {
			libVal := float64(libMDCT[i])
			gopusVal := gopusMDCT[i]
			diff := gopusVal - libVal
			absDiff := math.Abs(diff)
			if absDiff > maxDiff {
				maxDiff = absDiff
				maxDiffIdx = i
			}
			errPow += diff * diff
			sigPow += libVal * libVal
		}

		snr := 200.0
		if errPow > 0 && sigPow > 0 {
			snr = 10 * math.Log10(sigPow/errPow)
		}

		t.Logf("Block %d: SNR=%.2f dB, maxDiff=%.4f at coeff %d", b, snr, maxDiff, maxDiffIdx)
		t.Log("")

		if snr < 60 {
			t.Logf("WARNING: Block %d has poor SNR (%.2f dB < 60 dB)", b, snr)
		}
	}

	// Now compare the full interleaved output
	t.Log("=== Full Frame MDCT (Interleaved) ===")

	// Gopus full short MDCT
	gopusFullMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, enc.OverlapBuffer(), shortBlocks)

	// For libopus, we need to manually interleave the short block outputs
	libFullMDCT := make([]float64, frameSize)
	for b := 0; b < shortBlocks; b++ {
		blockStart := b * shortSize
		libBlockInput := make([]float32, shortSize+overlap)
		for i := 0; i < shortSize+overlap && blockStart+i < len(libInput); i++ {
			libBlockInput[i] = libInput[blockStart+i]
		}
		blockMDCT := mode.MDCTForward(libBlockInput, shift)

		// Interleave: coefficient i from block b goes to output[b + i*shortBlocks]
		for i, v := range blockMDCT {
			outIdx := b + i*shortBlocks
			if outIdx < frameSize {
				libFullMDCT[outIdx] = float64(v)
			}
		}
	}

	t.Log("First 20 interleaved coefficients:")
	t.Log("Coeff | Gopus        | Libopus      | Diff")
	t.Log("------+--------------+--------------+---------")

	var fullErrPow, fullSigPow float64
	fullMaxDiff := 0.0
	fullMaxDiffIdx := 0

	for i := 0; i < 20 && i < len(gopusFullMDCT) && i < len(libFullMDCT); i++ {
		diff := gopusFullMDCT[i] - libFullMDCT[i]
		absDiff := math.Abs(diff)
		if absDiff > fullMaxDiff {
			fullMaxDiff = absDiff
			fullMaxDiffIdx = i
		}
		fullErrPow += diff * diff
		fullSigPow += libFullMDCT[i] * libFullMDCT[i]
		t.Logf("%5d | %12.4f | %12.4f | %8.4f", i, gopusFullMDCT[i], libFullMDCT[i], diff)
	}

	// Complete the SNR calculation
	for i := 20; i < len(gopusFullMDCT) && i < len(libFullMDCT); i++ {
		diff := gopusFullMDCT[i] - libFullMDCT[i]
		absDiff := math.Abs(diff)
		if absDiff > fullMaxDiff {
			fullMaxDiff = absDiff
			fullMaxDiffIdx = i
		}
		fullErrPow += diff * diff
		fullSigPow += libFullMDCT[i] * libFullMDCT[i]
	}

	fullSNR := 200.0
	if fullErrPow > 0 && fullSigPow > 0 {
		fullSNR = 10 * math.Log10(fullSigPow/fullErrPow)
	}

	t.Logf("Full frame: SNR=%.2f dB, maxDiff=%.4f at coeff %d", fullSNR, fullMaxDiff, fullMaxDiffIdx)

	// Now compare band energies using the same MDCT outputs
	t.Log("")
	t.Log("=== Band Energies from MDCT ===")

	mode2 := celt.GetModeConfig(frameSize)
	nbBands := mode2.EffBands
	lm := mode2.LM

	// Gopus band energies (from gopus MDCT)
	gopusBandE := enc.ComputeBandEnergies(gopusFullMDCT, nbBands, frameSize)

	// Libopus-style band energies (from libopus MDCT interleaved)
	libFullMDCTF32 := make([]float32, len(libFullMDCT))
	for i, v := range libFullMDCT {
		libFullMDCTF32[i] = float32(v)
	}
	libBandE := ComputeLibopusBandEnergies(libFullMDCTF32, nbBands, frameSize, lm)

	t.Log("Band | Gopus Energy | Libopus Energy | Diff")
	t.Log("-----+--------------+----------------+------")

	energyMaxDiff := 0.0
	for band := 0; band < nbBands; band++ {
		diff := math.Abs(gopusBandE[band] - float64(libBandE[band]))
		if diff > energyMaxDiff {
			energyMaxDiff = diff
		}
		t.Logf("%4d | %12.4f | %14.4f | %.4f", band, gopusBandE[band], libBandE[band], diff)
	}

	t.Logf("Max band energy difference: %.6f (%.2f dB)", energyMaxDiff, energyMaxDiff*6)

	if fullSNR < 60 {
		t.Errorf("Short block MDCT SNR too low: %.2f dB (expected >= 60 dB)", fullSNR)
	}
	if energyMaxDiff > 0.1 {
		t.Errorf("Band energy difference too high: %.4f (expected < 0.1)", energyMaxDiff)
	}
}
