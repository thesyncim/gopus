// overlap_trace_test.go - Trace overlap buffer state frame by frame
package cgo

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/thesyncim/gopus"
)

func TestOverlapBufferTraceFrameByFrame(t *testing.T) {
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

	goDec, _ := gopus.NewDecoder(48000, 2)
	celtDec := goDec.GetCELTDecoder()

	t.Logf("Initial overlap buffer length: %d", len(celtDec.OverlapBuffer()))

	// Check initial state
	overlap := celtDec.OverlapBuffer()
	t.Logf("Initial: first10=%v last10=%v", overlap[:10], overlap[len(overlap)-10:])

	// Decode first 5 packets and check overlap each time
	for i := 0; i < 5 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		goDec.DecodeFloat32(pkt)
		overlap = celtDec.OverlapBuffer()

		// Check if L and R portions are different
		var lEnergy, rEnergy float64
		for j := 0; j < 120; j++ {
			lEnergy += overlap[j] * overlap[j]
			rEnergy += overlap[120+j] * overlap[120+j]
		}

		// Check if L == R (should be true for mono packets decoded to stereo)
		var diffSum float64
		for j := 0; j < 120; j++ {
			diff := overlap[j] - overlap[120+j]
			diffSum += diff * diff
		}

		t.Logf("Pkt %d: stereo=%v frame=%d | L energy=%.2f, R energy=%.2f, L!=R diff=%.2f",
			i, toc.Stereo, toc.FrameSize, lEnergy, rEnergy, diffSum)

		if i < 3 {
			t.Logf("  L first 5: %v", overlap[:5])
			t.Logf("  R first 5: %v", overlap[120:125])
		}
	}

	// Skip to packet 60-65 area
	for i := 5; i < 58 && i < len(packets); i++ {
		goDec.DecodeFloat32(packets[i])
	}

	// Detailed trace for packets 58-65
	t.Log("\nDetailed trace for packets 58-65:")
	for i := 58; i <= 65 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Get overlap BEFORE decoding
		overlapBefore := make([]float64, len(celtDec.OverlapBuffer()))
		copy(overlapBefore, celtDec.OverlapBuffer())

		goDec.DecodeFloat32(pkt)

		// Get overlap AFTER decoding
		overlapAfter := celtDec.OverlapBuffer()

		var lEnergyBefore, rEnergyBefore float64
		var lEnergyAfter, rEnergyAfter float64
		for j := 0; j < 120; j++ {
			lEnergyBefore += overlapBefore[j] * overlapBefore[j]
			rEnergyBefore += overlapBefore[120+j] * overlapBefore[120+j]
			lEnergyAfter += overlapAfter[j] * overlapAfter[j]
			rEnergyAfter += overlapAfter[120+j] * overlapAfter[120+j]
		}

		var diffBefore, diffAfter float64
		for j := 0; j < 120; j++ {
			diff := overlapBefore[j] - overlapBefore[120+j]
			diffBefore += diff * diff
			diff = overlapAfter[j] - overlapAfter[120+j]
			diffAfter += diff * diff
		}

		t.Logf("Pkt %d: stereo=%v frame=%d", i, toc.Stereo, toc.FrameSize)
		t.Logf("  Before: L=%.2f R=%.2f L!=R=%.2f", lEnergyBefore, rEnergyBefore, diffBefore)
		t.Logf("  After:  L=%.2f R=%.2f L!=R=%.2f", lEnergyAfter, rEnergyAfter, diffAfter)

		// Show actual values
		if rEnergyAfter < 0.001 && lEnergyAfter > 0.001 {
			t.Logf("  WARNING: R channel overlap appears to be zero!")
			t.Logf("  L[0:5]: %v", overlapAfter[:5])
			t.Logf("  R[0:5]: %v", overlapAfter[120:125])
		}
	}
}

// TestCheckOverlapBufferUpdate verifies that SynthesizeStereo actually updates both channels
func TestCheckOverlapBufferUpdate(t *testing.T) {
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

	// Create fresh decoder
	goDec, _ := gopus.NewDecoder(48000, 2)
	celtDec := goDec.GetCELTDecoder()

	// Decode just the first packet
	pkt := packets[0]
	toc := gopus.ParseTOC(pkt[0])

	t.Logf("First packet: stereo=%v frame=%d len=%d", toc.Stereo, toc.FrameSize, len(pkt))

	// Check overlap before
	overlapBefore := make([]float64, len(celtDec.OverlapBuffer()))
	copy(overlapBefore, celtDec.OverlapBuffer())

	var lZeros, rZeros int
	for j := 0; j < 120; j++ {
		if overlapBefore[j] == 0 {
			lZeros++
		}
		if overlapBefore[120+j] == 0 {
			rZeros++
		}
	}
	t.Logf("Before decode: L zeros=%d/120, R zeros=%d/120", lZeros, rZeros)

	// Decode
	goDec.DecodeFloat32(pkt)

	// Check overlap after
	overlapAfter := celtDec.OverlapBuffer()

	lZeros, rZeros = 0, 0
	for j := 0; j < 120; j++ {
		if overlapAfter[j] == 0 {
			lZeros++
		}
		if overlapAfter[120+j] == 0 {
			rZeros++
		}
	}
	t.Logf("After decode: L zeros=%d/120, R zeros=%d/120", lZeros, rZeros)

	if rZeros == 120 && lZeros < 120 {
		t.Errorf("BUG: R channel overlap not being updated! L has data but R is all zeros")
	}

	// Also check if L == R for mono packet
	if !toc.Stereo {
		var diffSum float64
		for j := 0; j < 120; j++ {
			diff := overlapAfter[j] - overlapAfter[120+j]
			diffSum += diff * diff
		}
		if diffSum > 0.0001 {
			t.Errorf("For mono packet, L and R overlap should be identical, but diff sum = %.6f", diffSum)
			t.Logf("  L[0:5]: %v", overlapAfter[:5])
			t.Logf("  R[0:5]: %v", overlapAfter[120:125])
		}
	}
}
