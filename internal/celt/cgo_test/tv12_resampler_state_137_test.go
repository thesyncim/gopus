// Package cgo traces resampler state at packet 137.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResamplerState137 traces resampler state at packet 137.
func TestTV12ResamplerState137(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder at 48kHz
	goDec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(48000, 1))
	silkDec := goDec.GetSILKDecoder()

	t.Log("=== Tracing resampler state around packet 137 ===")

	// Process packets 0-136 to build state
	for i := 0; i < 137; i++ {
		decodeFloat32(goDec, packets[i])
	}

	// Check NB resampler state (should have state from processing)
	nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
	if nbRes != nil {
		sIIR := nbRes.GetSIIR()
		sFIR := nbRes.GetSFIR()
		t.Logf("NB resampler (after pkt 136): sIIR=[%d, %d, %d, %d, %d, %d]",
			sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])
		t.Logf("  sFIR=[%d, %d, %d, ...]", sFIR[0], sFIR[1], sFIR[2])
	} else {
		t.Log("NB resampler: not created")
	}

	// Check MB resampler state BEFORE packet 137
	mbRes := silkDec.GetResampler(silk.BandwidthMediumband)
	if mbRes != nil {
		sIIR := mbRes.GetSIIR()
		sFIR := mbRes.GetSFIR()
		t.Logf("MB resampler (before pkt 137): sIIR=[%d, %d, %d, %d, %d, %d]",
			sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])
		t.Logf("  sFIR=[%d, %d, %d, ...]", sFIR[0], sFIR[1], sFIR[2])
	} else {
		t.Log("MB resampler: not created yet (will be created at packet 137)")
	}

	// Now decode packet 137
	t.Log("\n=== Decoding packet 137 (first MB) ===")
	pkt := packets[137]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("Packet 137: Mode=%v BW=%d", toc.Mode, toc.Bandwidth)

	goOut, _ := decodeFloat32(goDec, pkt)
	t.Logf("Output samples: %d", len(goOut))
	t.Log("First 10 output samples:")
	for i := 0; i < 10 && i < len(goOut); i++ {
		t.Logf("  [%d] %.6f", i, goOut[i])
	}

	// Check MB resampler state AFTER packet 137
	mbRes = silkDec.GetResampler(silk.BandwidthMediumband)
	if mbRes != nil {
		sIIR := mbRes.GetSIIR()
		sFIR := mbRes.GetSFIR()
		t.Logf("MB resampler (after pkt 137): sIIR=[%d, %d, %d, %d, %d, %d]",
			sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])
		t.Logf("  sFIR=[%d, %d, %d, ...]", sFIR[0], sFIR[1], sFIR[2])
	}

	// Decode packet 138 and check state
	decodeFloat32(goDec, packets[138])
	mbRes = silkDec.GetResampler(silk.BandwidthMediumband)
	if mbRes != nil {
		sIIR := mbRes.GetSIIR()
		t.Logf("MB resampler (after pkt 138): sIIR=[%d, %d, %d, %d, %d, %d]",
			sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])
	}
}
