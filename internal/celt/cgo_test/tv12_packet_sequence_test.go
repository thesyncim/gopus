// Package cgo checks the packet sequence around packet 826.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12PacketSequence shows the mode sequence around packet 826.
func TestTV12PacketSequence(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 850)
	if err != nil {
		t.Skip("Could not load packets")
	}

	t.Log("Packet sequence around 826:")
	t.Log("(Showing packets 810-835)")

	for i := 810; i <= 835 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		modeStr := "?"
		switch toc.Mode {
		case gopus.ModeSILK:
			modeStr = "SILK"
		case gopus.ModeHybrid:
			modeStr = "Hybrid"
		case gopus.ModeCELT:
			modeStr = "CELT"
		}

		bwStr := "?"
		switch toc.Bandwidth {
		case gopus.BandwidthNarrowband:
			bwStr = "NB"
		case gopus.BandwidthMediumband:
			bwStr = "MB"
		case gopus.BandwidthWideband:
			bwStr = "WB"
		case gopus.BandwidthSuperwideband:
			bwStr = "SWB"
		case gopus.BandwidthFullband:
			bwStr = "FB"
		}

		marker := ""
		if i == 826 {
			marker = " <-- TARGET"
		}

		t.Logf("Packet %d: Mode=%s, BW=%s, FrameSize=%d%s",
			i, modeStr, bwStr, toc.FrameSize, marker)
	}

	// Find the last SILK packet before 826
	t.Log("\nFinding last SILK packet before 826:")
	for i := 825; i >= 0; i-- {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode == gopus.ModeSILK {
			bwStr := "?"
			switch toc.Bandwidth {
			case gopus.BandwidthNarrowband:
				bwStr = "NB"
			case gopus.BandwidthMediumband:
				bwStr = "MB"
			case gopus.BandwidthWideband:
				bwStr = "WB"
			}
			t.Logf("Last SILK before 826: packet %d, BW=%s", i, bwStr)
			break
		}
	}
}
