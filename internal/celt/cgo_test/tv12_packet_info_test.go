// Package cgo provides TV12 packet analysis.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12PacketInfo shows TOC details for TV12 packets around the worst ones.
func TestTV12PacketInfo(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 1200)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Check packets around 826
	t.Log("Packets around 826:")
	for i := 820; i <= 830 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		t.Logf("  Packet %d: Mode=%v BW=%v FrameSize=%d Stereo=%v TOC=0x%02X",
			i, toc.Mode, toc.Bandwidth, toc.FrameSize, toc.Stereo, pkt[0])
	}

	// Show bandwidth changes around 826
	t.Log("\nBandwidth changes in TV12:")
	prevBW := gopus.Bandwidth(255)
	for i := 0; i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Bandwidth != prevBW {
			t.Logf("  Packet %d: BW changed from %v to %v", i, prevBW, toc.Bandwidth)
			prevBW = toc.Bandwidth
		}
	}

	// Check mode distribution
	t.Log("\nMode distribution:")
	silkCount := 0
	hybridCount := 0
	celtCount := 0
	for _, pkt := range packets {
		toc := gopus.ParseTOC(pkt[0])
		switch toc.Mode {
		case gopus.ModeSILK:
			silkCount++
		case gopus.ModeHybrid:
			hybridCount++
		case gopus.ModeCELT:
			celtCount++
		}
	}
	t.Logf("  SILK: %d, Hybrid: %d, CELT: %d", silkCount, hybridCount, celtCount)
}
