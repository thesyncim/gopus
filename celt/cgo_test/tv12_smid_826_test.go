//go:build cgo_libopus
// +build cgo_libopus

// Package cgo traces sMid handling at packet 826.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/silk"
)

// TestTV12SMid826 traces sMid at packet 826 and checks resampler input.
func TestTV12SMid826(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	silkDec := silk.NewDecoder()

	// Track sMid values around packet 826
	t.Log("=== Tracing sMid around packet 826 ===")

	// Process packets 0-824
	for i := 0; i <= 824; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if ok {
			silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		}
	}

	// Decode packet 825 (last MB before NB)
	pkt825 := packets[825]
	toc825 := gopus.ParseTOC(pkt825[0])
	silkBW825, _ := silk.BandwidthFromOpus(int(toc825.Bandwidth))
	bwNames := []string{"NB", "MB", "WB"}
	t.Logf("Packet 825: BW=%s", bwNames[silkBW825])

	sMidBefore825 := silkDec.GetSMid()
	t.Logf("sMid BEFORE pkt 825: [%d, %d]", sMidBefore825[0], sMidBefore825[1])

	output825, _ := silkDec.Decode(pkt825[1:], silkBW825, toc825.FrameSize, true)

	sMidAfter825 := silkDec.GetSMid()
	t.Logf("sMid AFTER pkt 825: [%d, %d]", sMidAfter825[0], sMidAfter825[1])

	// Show last 10 native samples of packet 825 (what should be in sMid)
	t.Log("\nPacket 825 last 10 native samples (48kHz output):")
	for i := len(output825) - 10; i < len(output825); i++ {
		t.Logf("  [%d] %.6f (int16: %d)", i, output825[i], int16(output825[i]*32768))
	}

	// Now decode packet 826 (first NB)
	pkt826 := packets[826]
	toc826 := gopus.ParseTOC(pkt826[0])
	silkBW826, _ := silk.BandwidthFromOpus(int(toc826.Bandwidth))
	t.Logf("\nPacket 826: BW=%s (transition from %s)", bwNames[silkBW826], bwNames[silkBW825])

	sMidBefore826 := silkDec.GetSMid()
	t.Logf("sMid BEFORE pkt 826: [%d, %d]", sMidBefore826[0], sMidBefore826[1])
	t.Logf("  First resampler input will be: sMid[1]/32768 = %.6f", float32(sMidBefore826[1])/32768.0)

	output826, _ := silkDec.Decode(pkt826[1:], silkBW826, toc826.FrameSize, true)

	sMidAfter826 := silkDec.GetSMid()
	t.Logf("sMid AFTER pkt 826: [%d, %d]", sMidAfter826[0], sMidAfter826[1])

	// Show first 10 48kHz output samples
	t.Log("\nPacket 826 first 10 output samples (48kHz):")
	for i := 0; i < 10 && i < len(output826); i++ {
		t.Logf("  [%d] %.6f", i, output826[i])
	}

	// The key insight: sMid[1] from MB (12kHz) becomes the first input to NB (8kHz) resampler
	// If libopus uses different sMid values, that explains the divergence
	t.Log("\n=== Analysis ===")
	t.Logf("Expected first resampler input (from sMid[1]): %.6f", float32(sMidBefore826[1])/32768.0)
	t.Logf("Actual first 48kHz output: %.6f", output826[0])
	t.Log("If libopus has larger sMid values, its first output would be larger")
}
