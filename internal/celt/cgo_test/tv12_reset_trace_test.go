// Package cgo traces exactly when the resampler reset happens.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResamplerResetTiming traces the exact timing of resampler reset.
func TestTV12ResamplerResetTiming(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	t.Log("=== Tracing Full Decode Path at Packet 826 ===")

	// Process packets 0-825 (skip Hybrid since we're testing SILK-only path)
	for i := 0; i <= 825 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue // Skip Hybrid for this test
		}

		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	}

	// Check NB resampler state BEFORE packet 826
	nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
	sIIRBefore := nbRes.GetSIIR()
	t.Logf("BEFORE Decode(826): NB sIIR[0:3]=[%d,%d,%d]", sIIRBefore[0], sIIRBefore[1], sIIRBefore[2])
	t.Logf("BEFORE Decode(826): sMid=[%d,%d]", silkDec.GetSMid()[0], silkDec.GetSMid()[1])

	// Decode packet 826 through the FULL Decode path (which calls handleBandwidthChange)
	pkt826 := packets[826]
	toc := gopus.ParseTOC(pkt826[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	t.Logf("Packet 826: Mode=%v, BW=%v (%v)", toc.Mode, toc.Bandwidth, silkBW)

	output, err := silkDec.Decode(pkt826[1:], silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	// Check state AFTER decode
	sIIRAfter := nbRes.GetSIIR()
	t.Logf("AFTER Decode(826): NB sIIR[0:3]=[%d,%d,%d]", sIIRAfter[0], sIIRAfter[1], sIIRAfter[2])
	t.Logf("AFTER Decode(826): sMid=[%d,%d]", silkDec.GetSMid()[0], silkDec.GetSMid()[1])

	t.Logf("Output: len=%d, first 5: [%.6f, %.6f, %.6f, %.6f, %.6f]",
		len(output), output[0], output[1], output[2], output[3], output[4])

	// Compare with libopus
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process packets 0-825 through libopus (skip Hybrid since we're testing SILK-only path)
	for i := 0; i <= 825 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}
		libDec.DecodeFloat(pkt, 1920)
	}

	// Decode packet 826 with libopus
	libOut, libN := libDec.DecodeFloat(pkt826, 1920)
	t.Logf("Libopus: len=%d, first 5: [%.6f, %.6f, %.6f, %.6f, %.6f]",
		libN, libOut[0], libOut[1], libOut[2], libOut[3], libOut[4])

	// Calculate ratio
	t.Logf("\nComparison:")
	for i := 0; i < 10 && i < len(output) && i < libN; i++ {
		ratio := float32(0)
		if libOut[i] != 0 {
			ratio = output[i] / libOut[i]
		}
		t.Logf("  [%d] go=%.6f lib=%.6f ratio=%.3f", i, output[i], libOut[i], ratio)
	}
}
