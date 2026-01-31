// Package cgo checks TV12 packet types
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
)

func TestTV12PacketTypes(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 200)
	if err != nil {
		t.Skip("Could not load packets")
	}

	var silkCount, hybridCount, celtCount int
	prevMode := gopus.Mode(255)
	prevBW := gopus.Bandwidth(255)

	for i, pkt := range packets {
		if len(pkt) == 0 {
			continue
		}
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != prevMode || toc.Bandwidth != prevBW {
			bwNames := []string{"NB", "MB", "WB", "SWB", "FB"}
			bwName := "?"
			if int(toc.Bandwidth) < len(bwNames) {
				bwName = bwNames[toc.Bandwidth]
			}
			t.Logf("Packet %d: Mode=%v BW=%d (%s)", i, toc.Mode, toc.Bandwidth, bwName)
			prevMode = toc.Mode
			prevBW = toc.Bandwidth
		}

		switch toc.Mode {
		case gopus.ModeSILK:
			silkCount++
		case gopus.ModeHybrid:
			hybridCount++
		case gopus.ModeCELT:
			celtCount++
		}
	}

	t.Logf("\nTotal packets: %d", len(packets))
	t.Logf("SILK: %d, Hybrid: %d, CELT: %d", silkCount, hybridCount, celtCount)
}
