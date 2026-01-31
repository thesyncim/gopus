// Package cgo traces packet 826 using the actual Decode flow.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Packet826ActualFlow traces packet 826 using actual Decode() calls.
func TestTV12Packet826ActualFlow(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Process packets 0-825 to build state
	t.Log("Building state from packets 0-825...")
	for i := 0; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		// Use full Decode
		_, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
		}

		// Track last packet bandwidth
		if i == 825 {
			bwName := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}[silkBW]
			t.Logf("Packet 825: %s", bwName)

			// Check NB resampler state after packet 825
			nbResampler := silkDec.GetResampler(silk.BandwidthNarrowband)
			if nbResampler != nil {
				t.Logf("NB Resampler after packet 825:")
				t.Logf("  sIIR: %v", nbResampler.GetSIIR())
				t.Logf("  sFIR: %v", nbResampler.GetSFIR())
			}
		}
	}

	// Get sMid before packet 826
	sMidBefore := silkDec.GetSMid()
	t.Logf("\nsMid BEFORE packet 826: [%d, %d]", sMidBefore[0], sMidBefore[1])

	// Check NB resampler state BEFORE decoding packet 826
	nbResamplerBefore := silkDec.GetResampler(silk.BandwidthNarrowband)
	if nbResamplerBefore != nil {
		t.Logf("NB Resampler BEFORE packet 826 decode:")
		t.Logf("  sIIR: %v", nbResamplerBefore.GetSIIR())
		t.Logf("  sFIR: %v", nbResamplerBefore.GetSFIR())
	}

	// Decode packet 826 using full Decode flow
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	bwName := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}[silkBW]

	t.Logf("\n=== Decoding packet 826: %s ===", bwName)

	output, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	t.Logf("Output length: %d", len(output))
	t.Logf("First 20 output samples:")
	for i := 0; i < 20 && i < len(output); i++ {
		t.Logf("  [%d] %.6f", i, output[i])
	}

	// Check NB resampler state AFTER decoding packet 826
	nbResamplerAfter := silkDec.GetResampler(silk.BandwidthNarrowband)
	if nbResamplerAfter != nil {
		t.Logf("\nNB Resampler AFTER packet 826 decode:")
		t.Logf("  sIIR: %v", nbResamplerAfter.GetSIIR())
		t.Logf("  sFIR: %v", nbResamplerAfter.GetSFIR())
	}

	// Get sMid after packet 826
	sMidAfter := silkDec.GetSMid()
	t.Logf("sMid AFTER packet 826: [%d, %d]", sMidAfter[0], sMidAfter[1])
}
