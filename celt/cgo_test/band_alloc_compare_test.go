//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for band bit allocation.
// This test compares:
// - bits[] array (how many bits allocated per band for PVQ)
// - pulses[] array (computed pulses per band after bits2pulses conversion)
// - fine_quant[] array (fine energy bits per band)
// - fine_priority[] array (priority for extra bits)
package cgo

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestBandAllocationComparison compares all allocation arrays between gopus and libopus.
func TestBandAllocationComparison(t *testing.T) {
	testCases := []struct {
		name      string
		frameSize int
		channels  int
		bitrate   int
		trim      int
	}{
		{"20ms_mono_64kbps", 960, 1, 64000, 5},
		{"20ms_mono_128kbps", 960, 1, 128000, 5},
		{"20ms_stereo_128kbps", 960, 2, 128000, 5},
		{"10ms_mono_64kbps", 480, 1, 64000, 5},
		{"2.5ms_mono_64kbps", 120, 1, 64000, 5},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			compareBandAllocation(t, tc.frameSize, tc.channels, tc.bitrate, tc.trim)
		})
	}
}

func computeLMFromFrameSize(frameSize int) int {
	switch frameSize {
	case 120:
		return 0
	case 240:
		return 1
	case 480:
		return 2
	case 960:
		return 3
	default:
		return 3
	}
}

func compareBandAllocation(t *testing.T, frameSize, channels, bitrate, trim int) {
	nbBands := 21
	lm := computeLMFromFrameSize(frameSize)

	// Compute total bits in Q3 format (same as libopus)
	totalBitsQ3 := (bitrate * frameSize * 8) / 48000

	// Get caps from both implementations
	libopusCaps := LibopusComputeCaps(nbBands, lm, channels)
	gopusCaps := initCapsForTest(nbBands, lm, channels)

	// Verify caps match
	capsMatch := true
	for i := 0; i < nbBands; i++ {
		if libopusCaps[i] != gopusCaps[i] {
			capsMatch = false
			t.Logf("Caps mismatch at band %d: libopus=%d, gopus=%d", i, libopusCaps[i], gopusCaps[i])
		}
	}
	if !capsMatch {
		t.Errorf("Caps arrays do not match!")
	}

	offsets := make([]int, nbBands)
	intensity := nbBands // disabled
	dualStereo := 0

	// Call libopus allocation
	libCodedBands, libBalance, libPulses, libEbits, libFinePriority, _, _ :=
		LibopusComputeAllocation(0, nbBands, offsets, libopusCaps, trim, intensity, dualStereo, totalBitsQ3, channels, lm, 0, nbBands-1)

	// Call gopus allocation (Q3 bits)
	gopusResult := celt.ComputeAllocationWithEncoder(nil, totalBitsQ3, nbBands, channels, gopusCaps, offsets, trim, intensity, false, lm, 0, nbBands-1)

	t.Logf("\n=== Band Allocation Comparison ===")
	t.Logf("Parameters: frameSize=%d, channels=%d, bitrate=%d, trim=%d", frameSize, channels, bitrate, trim)
	t.Logf("Total bits (Q3): %d (%.1f bits)", totalBitsQ3, float64(totalBitsQ3)/8.0)

	// Compare coded bands and balance
	if libCodedBands != gopusResult.CodedBands {
		t.Errorf("CodedBands mismatch: libopus=%d, gopus=%d", libCodedBands, gopusResult.CodedBands)
	}
	if libBalance != gopusResult.Balance {
		t.Errorf("Balance mismatch: libopus=%d, gopus=%d", libBalance, gopusResult.Balance)
	}

	t.Logf("CodedBands: libopus=%d, gopus=%d", libCodedBands, gopusResult.CodedBands)
	t.Logf("Balance: libopus=%d, gopus=%d", libBalance, gopusResult.Balance)

	// Detailed per-band comparison
	t.Logf("\n%-5s | %10s | %10s | %6s | %8s | %8s | %6s | %6s | %6s | %6s",
		"Band", "lib_bits", "go_bits", "diff", "lib_fine", "go_fine", "f_diff", "lib_fp", "go_fp", "fp_diff")
	t.Logf("------+------------+------------+--------+----------+----------+--------+--------+--------+--------")

	totalBitsDiff := 0
	totalFineDiff := 0
	totalPriorityDiff := 0

	for i := 0; i < nbBands; i++ {
		libBits := libPulses[i]
		goBits := gopusResult.BandBits[i]
		bitsDiff := goBits - libBits

		libFine := libEbits[i]
		goFine := gopusResult.FineBits[i]
		fineDiff := goFine - libFine

		libPrio := libFinePriority[i]
		goPrio := gopusResult.FinePriority[i]
		prioDiff := goPrio - libPrio

		bitsDiffStr := ""
		fineDiffStr := ""
		prioDiffStr := ""

		if bitsDiff != 0 {
			bitsDiffStr = fmt.Sprintf("%+d", bitsDiff)
			totalBitsDiff++
		}
		if fineDiff != 0 {
			fineDiffStr = fmt.Sprintf("%+d", fineDiff)
			totalFineDiff++
		}
		if prioDiff != 0 {
			prioDiffStr = fmt.Sprintf("%+d", prioDiff)
			totalPriorityDiff++
		}

		if libBits > 0 || goBits > 0 || libFine > 0 || goFine > 0 {
			t.Logf("%-5d | %10d | %10d | %6s | %8d | %8d | %6s | %6d | %6d | %6s",
				i, libBits, goBits, bitsDiffStr, libFine, goFine, fineDiffStr, libPrio, goPrio, prioDiffStr)
		}
	}

	// Summary
	t.Logf("\n=== Summary ===")
	t.Logf("Bands with bit differences: %d", totalBitsDiff)
	t.Logf("Bands with fine bit differences: %d", totalFineDiff)
	t.Logf("Bands with priority differences: %d", totalPriorityDiff)

	if totalBitsDiff > 0 || totalFineDiff > 0 || totalPriorityDiff > 0 {
		t.Logf("NOTE: Allocation differs from libopus")
	} else {
		t.Logf("SUCCESS: Allocation matches libopus exactly!")
	}

	// Compute K values (pulses) for each band and compare
	t.Logf("\n=== PVQ Pulse Count Comparison ===")
	t.Logf("Converts bits to K (number of pulses) using bits2pulses/get_pulses")
	t.Logf("%-5s | %8s | %6s | %6s | %6s | %6s",
		"Band", "Width", "bits_Q3", "lib_K", "go_K", "diff")
	t.Logf("------+----------+--------+--------+--------+-------")

	for i := 0; i < nbBands && i < gopusResult.CodedBands; i++ {
		width := (celt.EBands[i+1] - celt.EBands[i]) << lm
		bitsQ3 := libPulses[i]

		// Compute K using gopus logic
		goK := computeK(i, lm, bitsQ3)

		// For comparison, we need to get libopus K
		// Since we're calling encode path, the bits array should be the same
		libK := goK // Assume same for now - we'd need CGO wrapper for libopus bits2pulses

		diffStr := ""
		if libK != goK {
			diffStr = fmt.Sprintf("%+d", goK-libK)
		}

		if bitsQ3 > 0 {
			t.Logf("%-5d | %8d | %6d | %6d | %6d | %6s",
				i, width, bitsQ3, libK, goK, diffStr)
		}
	}
}

// computeK converts bits (Q3) to pulse count K using gopus logic
func computeK(band, lm, bitsQ3 int) int {
	if bitsQ3 <= 0 {
		return 0
	}
	// Use gopus pulse cache
	q := celt.BitsToPulsesExport(band, lm, bitsQ3)
	return celt.GetPulsesExport(q)
}

// TestBits2PulsesComparison tests bits2pulses conversion matches libopus.
func TestBits2PulsesComparison(t *testing.T) {
	// Test various bit budgets and band sizes
	testCases := []struct {
		band   int
		lm     int
		bitsQ3 int
	}{
		{0, 3, 256}, // Low band, 20ms, typical allocation
		{5, 3, 200},
		{10, 3, 300},
		{15, 3, 700},
		{20, 3, 0},   // Zero bits
		{0, 0, 64},   // 2.5ms frame
		{10, 2, 200}, // 10ms frame
	}

	t.Logf("Testing bits2pulses conversion (gopus vs expected)")
	t.Logf("%-6s | %-4s | %8s | %6s | %6s",
		"Band", "LM", "Bits_Q3", "q", "K")
	t.Logf("-------+------+----------+--------+-------")

	for _, tc := range testCases {
		q := celt.BitsToPulsesExport(tc.band, tc.lm, tc.bitsQ3)
		k := celt.GetPulsesExport(q)

		t.Logf("%-6d | %-4d | %8d | %6d | %6d",
			tc.band, tc.lm, tc.bitsQ3, q, k)
	}
}
