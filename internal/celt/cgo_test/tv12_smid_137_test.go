// Package cgo traces sMid values at packet 137 transition.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12SMidAt137 traces sMid values around the NB→MB transition.
func TestTV12SMidAt137(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder at 48kHz
	goDec, _ := gopus.NewDecoderDefault(48000, 1)
	silkDec := goDec.GetSILKDecoder()

	t.Log("=== Tracing sMid around packet 137 ===")

	for i := 134; i < 145 && i < len(packets); i++ {
		// Get sMid BEFORE decode
		sMidBefore := silkDec.GetSMid()

		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Decode
		goSamples, _ := decodeFloat32(goDec, pkt)

		// Get sMid AFTER decode
		sMidAfter := silkDec.GetSMid()

		bwName := "NB"
		switch toc.Bandwidth {
		case 1:
			bwName = "MB"
		}

		marker := ""
		if i == 137 {
			marker = " <-- BW CHANGE"
		}

		t.Logf("Packet %d (%s): sMid before=[%d, %d] after=[%d, %d] samples=%d%s",
			i, bwName, sMidBefore[0], sMidBefore[1], sMidAfter[0], sMidAfter[1], len(goSamples), marker)

		// For packet 137, show what the first resampler input would be
		if i == 137 {
			t.Logf("  First resampler input: sMid[1]=%d (%.6f)", sMidBefore[1], float32(sMidBefore[1])/32768.0)
			t.Logf("  First 48kHz output: %.6f", goSamples[0])
		}
	}
}
