// block_boundary_trace_test.go - Trace error at block boundaries in transient frame
package cgo

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

// TestBlockBoundaryError traces error at specific sample positions
func TestBlockBoundaryError(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector07.bit"
	data, err := os.ReadFile(bitFile)
	if err != nil {
		t.Skipf("Cannot read %s: %v", bitFile, err)
		return
	}

	var packets [][]byte
	offset := 0
	for offset < len(data)-8 {
		pktLen := binary.BigEndian.Uint32(data[offset:])
		offset += 4
		offset += 4
		if int(pktLen) <= 0 || offset+int(pktLen) > len(data) {
			break
		}
		packets = append(packets, data[offset:offset+int(pktLen)])
		offset += int(pktLen)
	}

	// Build up state
	goDec, _ := gopus.NewDecoder(48000, 2)
	libDec, _ := NewLibopusDecoder(48000, 2)
	defer libDec.Destroy()

	for i := 0; i < 61; i++ {
		goDec.DecodeFloat32(packets[i])
		libDec.DecodeFloat(packets[i], 5760)
	}

	// Decode frame 61 (transient)
	goPcm, _ := goDec.DecodeFloat32(packets[61])
	libPcm, _ := libDec.DecodeFloat(packets[61], 5760)

	// Focus on block boundaries (at 120, 240, 360, 480, 600, 720, 840)
	boundaries := []int{118, 119, 120, 121, 122, // Block 0/1 boundary
		478, 479, 480, 481, 482, // Block 3/4 boundary
		598, 599, 600, 601, 602, // Block 4/5 boundary (where error jumps)
		718, 719, 720, 721, 722} // Block 5/6 boundary

	t.Log("Sample comparison at block boundaries:")
	t.Log("Sample | Go (L)     | Lib (L)    | Diff (L)    | Go (R)     | Lib (R)    | Diff (R)")
	t.Log("-------+------------+------------+-------------+------------+------------+------------")

	for _, samp := range boundaries {
		idxL := samp * 2
		idxR := samp*2 + 1

		if idxL < len(goPcm) && idxR < len(libPcm)*2 {
			goL := goPcm[idxL]
			goR := goPcm[idxR]
			libL := libPcm[idxL]
			libR := libPcm[idxR]
			diffL := float64(goL) - float64(libL)
			diffR := float64(goR) - float64(libR)

			// Mark block boundaries
			marker := ""
			if samp == 120 || samp == 240 || samp == 360 || samp == 480 ||
				samp == 600 || samp == 720 || samp == 840 {
				marker = " <-- BOUNDARY"
			}

			t.Logf("%5d | %10.6f | %10.6f | %11.2e | %10.6f | %10.6f | %11.2e%s",
				samp, goL, libL, diffL, goR, libR, diffR, marker)
		}
	}

	// Also check the OVERLAP region values (positions 0-119)
	t.Log("\nOverlap region (samples 0-9):")
	for samp := 0; samp < 10; samp++ {
		idxL := samp * 2
		goL := goPcm[idxL]
		libL := libPcm[idxL]
		diffL := float64(goL) - float64(libL)
		t.Logf("  [%d] go=%.6f lib=%.6f diff=%.2e", samp, goL, libL, diffL)
	}

	// Check where in block 5 the error starts growing
	t.Log("\nDetailed view of block 5 (samples 600-719):")
	t.Log("  First 10 samples:")
	for samp := 600; samp < 610; samp++ {
		idxL := samp * 2
		goL := goPcm[idxL]
		libL := libPcm[idxL]
		diffL := float64(goL) - float64(libL)
		t.Logf("    [%d] go=%.6f lib=%.6f diff=%.2e", samp, goL, libL, diffL)
	}
	t.Log("  Last 10 samples:")
	for samp := 710; samp < 720; samp++ {
		idxL := samp * 2
		goL := goPcm[idxL]
		libL := libPcm[idxL]
		diffL := float64(goL) - float64(libL)
		t.Logf("    [%d] go=%.6f lib=%.6f diff=%.2e", samp, goL, libL, diffL)
	}

	// Find the maximum error position in block 5
	var maxErr float64
	maxErrPos := 0
	for samp := 600; samp < 720; samp++ {
		idxL := samp * 2
		goL := goPcm[idxL]
		libL := libPcm[idxL]
		diff := math.Abs(float64(goL) - float64(libL))
		if diff > maxErr {
			maxErr = diff
			maxErrPos = samp
		}
	}
	t.Logf("\nMax error in block 5: %.2e at sample %d", maxErr, maxErrPos)
}
