// Package cgo compares full Opus path vs SILK-only path at packet 826.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
)

// TestTV12FullPath826 compares full Opus decode vs SILK-only decode.
func TestTV12FullPath826(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Full Opus decoder
	opusDec, _ := gopus.NewDecoderDefault(48000, 1)

	t.Log("=== Processing all packets 0-825 with Opus decoder ===")

	// Count packet types
	silkCount, hybridCount, celtCount := 0, 0, 0
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		switch toc.Mode {
		case gopus.ModeSILK:
			silkCount++
		case gopus.ModeHybrid:
			hybridCount++
		case gopus.ModeCELT:
			celtCount++
		}
		opusDec.DecodeFloat32(pkt)
	}
	t.Logf("Packet counts: SILK=%d, Hybrid=%d, CELT=%d", silkCount, hybridCount, celtCount)

	// Get internal SILK decoder
	silkDec := opusDec.GetSILKDecoder()

	// Check sMid before packet 826
	sMidBefore := silkDec.GetSMid()
	t.Logf("\nsMid BEFORE pkt 826 (full path): [%d, %d]", sMidBefore[0], sMidBefore[1])

	// Decode packet 826
	pkt826 := packets[826]
	toc826 := gopus.ParseTOC(pkt826[0])
	bwNames := map[int]string{0: "NB", 1: "MB", 2: "WB", 3: "SWB", 4: "FB"}
	t.Logf("\nPacket 826: Mode=%v BW=%s", toc826.Mode, bwNames[int(toc826.Bandwidth)])

	output826, _ := opusDec.DecodeFloat32(pkt826)

	// Check sMid after packet 826
	sMidAfter := silkDec.GetSMid()
	t.Logf("sMid AFTER pkt 826: [%d, %d]", sMidAfter[0], sMidAfter[1])

	// Show first 10 output samples
	t.Log("\nFirst 10 output samples (48kHz):")
	for i := 0; i < 10 && i < len(output826); i++ {
		t.Logf("  [%d] %.6f", i, output826[i])
	}

	// Compare with libopus
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec != nil {
		defer libDec.Destroy()
		for i := 0; i <= 825; i++ {
			libDec.DecodeFloat(packets[i], 1920)
		}
		libOut, _ := libDec.DecodeFloat(pkt826, 1920)

		t.Log("\nLibopus first 10 samples (48kHz):")
		for i := 0; i < 10; i++ {
			t.Logf("  [%d] %.6f", i, libOut[i])
		}

		t.Log("\nDifferences:")
		for i := 0; i < 10 && i < len(output826); i++ {
			diff := output826[i] - libOut[i]
			t.Logf("  [%d] go=%.6f lib=%.6f diff=%.6f", i, output826[i], libOut[i], diff)
		}
	}
}
