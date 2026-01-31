// Package cgo tests the full decoder flow for packet 826 reset behavior.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Packet826FullFlowReset tests whether the resampler is properly reset
// when decoding packet 826 after processing all prior packets.
func TestTV12Packet826FullFlowReset(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	// Process ALL packets 0-825 like the real decoder would
	t.Log("Processing ALL packets 0-825...")
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

		silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	}

	// Check NB resampler state after all packets
	nbResampler := silkDec.GetResampler(silk.BandwidthNarrowband)
	t.Logf("\nNB Resampler after packet 825:")
	if nbResampler != nil {
		t.Logf("  sIIR: %v", nbResampler.GetSIIR())
		t.Logf("  sFIR: %v", nbResampler.GetSFIR())
	}

	// Check MB resampler state after all packets
	mbResampler := silkDec.GetResampler(silk.BandwidthMediumband)
	t.Logf("MB Resampler after packet 825:")
	if mbResampler != nil {
		t.Logf("  sIIR: %v", mbResampler.GetSIIR())
		t.Logf("  sFIR: %v", mbResampler.GetSFIR())
	}

	// Now decode packet 826 (NB after MB)
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	t.Logf("\n=== Decoding packet 826: BW=%v ===", silkBW)

	output, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Check NB resampler state AFTER packet 826
	nbResamplerAfter := silkDec.GetResampler(silk.BandwidthNarrowband)
	t.Logf("\nNB Resampler AFTER packet 826:")
	if nbResamplerAfter != nil {
		t.Logf("  sIIR: %v", nbResamplerAfter.GetSIIR())
		t.Logf("  sFIR: %v", nbResamplerAfter.GetSFIR())
	}

	// Show output
	t.Logf("\nGopus output (first 30):")
	for i := 0; i < 30 && i < len(output); i++ {
		t.Logf("  [%2d] %.9f", i, output[i])
	}

	// Compare with libopus
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process all packets with libopus too
	for i := 0; i <= 825; i++ {
		libDec.DecodeFloat(packets[i], 1920)
	}
	libOut, _ := libDec.DecodeFloat(packets[826], 1920)

	t.Logf("\nLibopus output (first 30):")
	for i := 0; i < 30 && i < len(libOut); i++ {
		t.Logf("  [%2d] %.9f", i, libOut[i])
	}

	// The key question: was the NB resampler reset?
	// If sIIR/sFIR before packet 826 = after, it wasn't reset properly.
	// If they're different (and the new values reflect fresh processing), it was reset.
}
