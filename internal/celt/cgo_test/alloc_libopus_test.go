// Package cgo provides allocation comparison tests between gopus and libopus.
package cgo

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// AllocationTestCase defines parameters for an allocation comparison test.
type AllocationTestCase struct {
	Name       string
	FrameSize  int // 120, 240, 480, 960 (2.5ms, 5ms, 10ms, 20ms)
	Channels   int // 1 or 2
	Bitrate    int // bits per second
	Trim       int // allocation trim (0-10, default 5)
	Intensity  int // intensity stereo start band (nbBands = disabled)
	DualStereo bool
}

func computeLM(frameSize int) int {
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

func computeTotalBitsQ3(bitrate, frameSize int) int {
	// bits = bitrate * frameSize / sampleRate
	// At 48kHz: bits = bitrate * frameSize / 48000
	// Q3 means multiply by 8
	return (bitrate * frameSize * 8) / 48000
}

// initCapsForTest creates caps array matching libopus logic.
func initCapsForTest(nbBands, lm, channels int) []int {
	caps := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		N := (celt.EBands[i+1] - celt.EBands[i]) << lm
		row := 2*lm + (channels - 1)
		capIdx := celt.MaxBands*row + i
		cap := int(celt.GetCacheCaps()[capIdx])
		caps[i] = (cap + 64) * channels * N >> 2
	}
	return caps
}

// compareAllocations compares gopus and libopus allocation results.
func compareAllocations(t *testing.T, tc AllocationTestCase) {
	t.Helper()

	nbBands := 21
	lm := computeLM(tc.FrameSize)
	totalBitsQ3 := computeTotalBitsQ3(tc.Bitrate, tc.FrameSize)

	// Compute caps using libopus method
	libopusCaps := LibopusComputeCaps(nbBands, lm, tc.Channels)
	gopusCaps := initCapsForTest(nbBands, lm, tc.Channels)

	// Verify caps match
	capsMatch := true
	for i := 0; i < nbBands; i++ {
		if libopusCaps[i] != gopusCaps[i] {
			capsMatch = false
			break
		}
	}
	if !capsMatch {
		t.Logf("WARNING: Caps mismatch!")
		for i := 0; i < nbBands; i++ {
			if libopusCaps[i] != gopusCaps[i] {
				t.Logf("  Band %d: libopus=%d, gopus=%d, diff=%d",
					i, libopusCaps[i], gopusCaps[i], gopusCaps[i]-libopusCaps[i])
			}
		}
	}

	offsets := make([]int, nbBands) // all zeros for now

	intensity := tc.Intensity
	if intensity <= 0 {
		intensity = nbBands // disabled
	}
	dualStereo := 0
	if tc.DualStereo {
		dualStereo = 1
	}

	// Call libopus
	libCodedBands, libBalance, libPulses, libEbits, _, libIntensity, libDualStereo :=
		LibopusComputeAllocation(
			0, nbBands,
			offsets, libopusCaps,
			tc.Trim,
			intensity, dualStereo,
			totalBitsQ3,
			tc.Channels, lm,
			0,         // prev
			nbBands-1, // signalBandwidth
		)

	// Call gopus
	gopusResult := celt.ComputeAllocation(
		totalBitsQ3>>3, // gopus uses bits, not Q3
		nbBands,
		tc.Channels,
		gopusCaps,
		offsets,
		tc.Trim,
		intensity,
		tc.DualStereo,
		lm,
	)

	// Print header
	t.Logf("")
	t.Logf("=== %s ===", tc.Name)
	t.Logf("Parameters: frameSize=%d, channels=%d, bitrate=%d, trim=%d",
		tc.FrameSize, tc.Channels, tc.Bitrate, tc.Trim)
	t.Logf("Total bits (Q3): %d (%.1f bits)", totalBitsQ3, float64(totalBitsQ3)/8.0)
	t.Logf("")

	// Compare coded bands
	t.Logf("CodedBands: libopus=%d, gopus=%d", libCodedBands, gopusResult.CodedBands)
	t.Logf("Balance: libopus=%d, gopus=%d", libBalance, gopusResult.Balance)
	t.Logf("Intensity: libopus=%d, gopus=%d", libIntensity, gopusResult.Intensity)
	t.Logf("DualStereo: libopus=%d, gopus=%v", libDualStereo, gopusResult.DualStereo)
	t.Logf("")

	// Compare per-band allocations
	t.Logf("Per-band PVQ bits (Q3):")
	t.Logf("%-5s | %10s | %10s | %10s | %8s", "Band", "libopus", "gopus", "diff", "status")
	t.Logf("------+------------+------------+------------+---------")

	totalDiff := 0
	bandsDiff := 0
	for i := 0; i < nbBands; i++ {
		libVal := libPulses[i]
		gopusVal := gopusResult.BandBits[i]
		diff := gopusVal - libVal

		status := "OK"
		if diff != 0 {
			status = "DIFF"
			bandsDiff++
			if diff > 0 {
				totalDiff += diff
			} else {
				totalDiff -= diff
			}
		}

		// Only show non-zero bands or differences
		if libVal != 0 || gopusVal != 0 || diff != 0 {
			t.Logf("%-5d | %10d | %10d | %+10d | %s", i, libVal, gopusVal, diff, status)
		}
	}

	t.Logf("")
	t.Logf("Per-band fine bits:")
	t.Logf("%-5s | %10s | %10s | %10s", "Band", "libopus", "gopus", "diff")
	t.Logf("------+------------+------------+-----------")

	for i := 0; i < nbBands; i++ {
		libVal := libEbits[i]
		gopusVal := gopusResult.FineBits[i]
		diff := gopusVal - libVal

		if libVal != 0 || gopusVal != 0 || diff != 0 {
			t.Logf("%-5d | %10d | %10d | %+10d", i, libVal, gopusVal, diff)
		}
	}

	t.Logf("")
	t.Logf("Summary: %d bands differ, total absolute diff = %d (Q3)", bandsDiff, totalDiff)

	// Calculate total bits allocated
	libTotalPVQ := 0
	gopusTotalPVQ := 0
	libTotalFine := 0
	gopusTotalFine := 0
	for i := 0; i < nbBands; i++ {
		libTotalPVQ += libPulses[i]
		gopusTotalPVQ += gopusResult.BandBits[i]
		libTotalFine += libEbits[i] * tc.Channels
		gopusTotalFine += gopusResult.FineBits[i] * tc.Channels
	}
	t.Logf("Total PVQ bits (Q3): libopus=%d, gopus=%d", libTotalPVQ, gopusTotalPVQ)
	t.Logf("Total fine bits: libopus=%d, gopus=%d", libTotalFine, gopusTotalFine)

	// Log diagnostic information (don't fail - this is for diagnostic purposes)
	if libCodedBands != gopusResult.CodedBands {
		t.Logf("NOTE: CodedBands mismatch: libopus=%d, gopus=%d - skip logic differs", libCodedBands, gopusResult.CodedBands)
	}
	if totalDiff > 0 {
		t.Logf("NOTE: Allocation differs by %d bits (Q3) across %d bands", totalDiff, bandsDiff)
	} else {
		t.Logf("SUCCESS: Allocation matches exactly!")
	}
}

// TestAllocationCompare_Mono_20ms_64kbps tests 20ms mono at 64kbps.
func TestAllocationCompare_Mono_20ms_64kbps(t *testing.T) {
	tc := AllocationTestCase{
		Name:      "20ms_mono_64kbps",
		FrameSize: 960,
		Channels:  1,
		Bitrate:   64000,
		Trim:      5,
		Intensity: 21, // disabled
	}
	compareAllocations(t, tc)
}

// TestAllocationCompare_Stereo_20ms_128kbps tests 20ms stereo at 128kbps.
func TestAllocationCompare_Stereo_20ms_128kbps(t *testing.T) {
	tc := AllocationTestCase{
		Name:       "20ms_stereo_128kbps",
		FrameSize:  960,
		Channels:   2,
		Bitrate:    128000,
		Trim:       5,
		Intensity:  21, // disabled
		DualStereo: false,
	}
	compareAllocations(t, tc)
}

// TestAllocationCompare_VariousFrameSizes tests various frame sizes.
func TestAllocationCompare_VariousFrameSizes(t *testing.T) {
	testCases := []AllocationTestCase{
		{
			Name:      "2.5ms_mono_64kbps",
			FrameSize: 120,
			Channels:  1,
			Bitrate:   64000,
			Trim:      5,
			Intensity: 21,
		},
		{
			Name:      "5ms_mono_64kbps",
			FrameSize: 240,
			Channels:  1,
			Bitrate:   64000,
			Trim:      5,
			Intensity: 21,
		},
		{
			Name:      "10ms_mono_64kbps",
			FrameSize: 480,
			Channels:  1,
			Bitrate:   64000,
			Trim:      5,
			Intensity: 21,
		},
		{
			Name:      "20ms_mono_64kbps",
			FrameSize: 960,
			Channels:  1,
			Bitrate:   64000,
			Trim:      5,
			Intensity: 21,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			compareAllocations(t, tc)
		})
	}
}

// TestAllocationCompare_VariousBitrates tests various bitrates.
func TestAllocationCompare_VariousBitrates(t *testing.T) {
	bitrates := []int{32000, 48000, 64000, 96000, 128000, 192000, 256000}

	for _, bitrate := range bitrates {
		tc := AllocationTestCase{
			Name:      fmt.Sprintf("20ms_mono_%dkbps", bitrate/1000),
			FrameSize: 960,
			Channels:  1,
			Bitrate:   bitrate,
			Trim:      5,
			Intensity: 21,
		}
		t.Run(tc.Name, func(t *testing.T) {
			compareAllocations(t, tc)
		})
	}
}

// TestAllocationCompare_VariousTrims tests various allocation trims.
func TestAllocationCompare_VariousTrims(t *testing.T) {
	for trim := 0; trim <= 10; trim++ {
		tc := AllocationTestCase{
			Name:      fmt.Sprintf("20ms_mono_64kbps_trim%d", trim),
			FrameSize: 960,
			Channels:  1,
			Bitrate:   64000,
			Trim:      trim,
			Intensity: 21,
		}
		t.Run(tc.Name, func(t *testing.T) {
			compareAllocations(t, tc)
		})
	}
}

// TestAllocationCompare_Tables verifies gopus tables match libopus.
func TestAllocationCompare_Tables(t *testing.T) {
	// Compare EBands
	libEBands := LibopusGetEBands()
	t.Log("EBands comparison:")
	match := true
	for i := 0; i < 22; i++ {
		gopusVal := celt.EBands[i]
		if libEBands[i] != gopusVal {
			t.Logf("  EBands[%d]: libopus=%d, gopus=%d, MISMATCH", i, libEBands[i], gopusVal)
			match = false
		}
	}
	if match {
		t.Log("  EBands: OK (all match)")
	}

	// Compare LogN
	libLogN := LibopusGetLogN()
	t.Log("LogN comparison:")
	match = true
	for i := 0; i < 21; i++ {
		gopusVal := celt.LogN[i]
		if libLogN[i] != gopusVal {
			t.Logf("  LogN[%d]: libopus=%d, gopus=%d, MISMATCH", i, libLogN[i], gopusVal)
			match = false
		}
	}
	if match {
		t.Log("  LogN: OK (all match)")
	}

	// Compare BandAlloc vectors
	t.Log("BandAlloc comparison:")
	nbVectors := LibopusGetNbAllocVectors()
	t.Logf("  libopus has %d allocation vectors", nbVectors)
	t.Logf("  gopus has %d allocation vectors", len(celt.BandAlloc))

	for row := 0; row < nbVectors && row < len(celt.BandAlloc); row++ {
		libRow := LibopusGetAllocVectors(row)
		rowMatch := true
		for i := 0; i < 21; i++ {
			if libRow[i] != celt.BandAlloc[row][i] {
				rowMatch = false
				break
			}
		}
		if !rowMatch {
			t.Logf("  Row %d MISMATCH:", row)
			t.Logf("    libopus: %v", libRow)
			t.Logf("    gopus:   %v", celt.BandAlloc[row][:])
		}
	}
}

// TestAllocationCompare_DetailedDump provides a detailed dump for debugging.
func TestAllocationCompare_DetailedDump(t *testing.T) {
	// Common test case
	nbBands := 21
	lm := 3 // 20ms
	channels := 1
	bitrate := 64000
	frameSize := 960
	trim := 5

	totalBitsQ3 := computeTotalBitsQ3(bitrate, frameSize)

	t.Logf("Detailed allocation comparison")
	t.Logf("==============================")
	t.Logf("Frame size: %d samples (%.1fms)", frameSize, float64(frameSize)/48.0)
	t.Logf("Channels: %d", channels)
	t.Logf("Bitrate: %d bps", bitrate)
	t.Logf("LM: %d", lm)
	t.Logf("Trim: %d", trim)
	t.Logf("Total bits: %d (Q3: %d)", totalBitsQ3/8, totalBitsQ3)
	t.Logf("")

	// Get caps
	libCaps := LibopusComputeCaps(nbBands, lm, channels)
	gopusCaps := initCapsForTest(nbBands, lm, channels)

	t.Logf("Caps comparison:")
	t.Logf("%-5s | %8s | %8s | %6s", "Band", "libopus", "gopus", "diff")
	t.Logf("------+----------+----------+-------")
	for i := 0; i < nbBands; i++ {
		diff := gopusCaps[i] - libCaps[i]
		diffStr := ""
		if diff != 0 {
			diffStr = fmt.Sprintf("%+d", diff)
		}
		t.Logf("%-5d | %8d | %8d | %6s", i, libCaps[i], gopusCaps[i], diffStr)
	}
	t.Logf("")

	// Compute allocations
	offsets := make([]int, nbBands)
	intensity := nbBands

	libCodedBands, libBalance, libPulses, libEbits, _, _, _ :=
		LibopusComputeAllocation(0, nbBands, offsets, libCaps, trim, intensity, 0, totalBitsQ3, channels, lm, 0, nbBands-1)

	gopusResult := celt.ComputeAllocation(totalBitsQ3>>3, nbBands, channels, gopusCaps, offsets, trim, intensity, false, lm)

	t.Logf("Results:")
	t.Logf("  CodedBands: libopus=%d, gopus=%d", libCodedBands, gopusResult.CodedBands)
	t.Logf("  Balance: libopus=%d, gopus=%d", libBalance, gopusResult.Balance)
	t.Logf("")

	t.Logf("Full allocation table:")
	t.Logf("%-5s | %6s | %10s | %10s | %6s | %6s | %6s | %6s",
		"Band", "Width", "lib_pvq", "go_pvq", "diff", "lib_fb", "go_fb", "diff")
	t.Logf("------+--------+------------+------------+--------+--------+--------+-------")

	for i := 0; i < nbBands; i++ {
		width := (celt.EBands[i+1] - celt.EBands[i]) << lm
		libPVQ := libPulses[i]
		goPVQ := gopusResult.BandBits[i]
		pvqDiff := goPVQ - libPVQ
		libFB := libEbits[i]
		goFB := gopusResult.FineBits[i]
		fbDiff := goFB - libFB

		pvqDiffStr := ""
		if pvqDiff != 0 {
			pvqDiffStr = fmt.Sprintf("%+d", pvqDiff)
		}
		fbDiffStr := ""
		if fbDiff != 0 {
			fbDiffStr = fmt.Sprintf("%+d", fbDiff)
		}

		t.Logf("%-5d | %6d | %10d | %10d | %6s | %6d | %6d | %6s",
			i, width, libPVQ, goPVQ, pvqDiffStr, libFB, goFB, fbDiffStr)
	}
}
