// Package cgo provides detailed trace for TV12 packet 826 decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Packet826DetailedTrace traces the exact decode flow.
func TestTV12Packet826DetailedTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create libopus 48kHz decoder for reference
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Create gopus SILK decoder
	silkDec := silk.NewDecoder()

	// Track bandwidth changes
	var prevBW silk.Bandwidth
	var prevBWSet bool

	// Process packets 820-825 (last few before 826)
	t.Log("=== Processing packets 820-825 ===")
	for i := 820; i <= 825; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		// Libopus decode
		libDec.DecodeFloat(pkt, 1920)

		if toc.Mode != gopus.ModeSILK {
			t.Logf("Packet %d: Mode=%v (skipping SILK)", i, toc.Mode)
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}

		// Log bandwidth changes
		if prevBWSet && prevBW != silkBW {
			t.Logf("Packet %d: BANDWIDTH CHANGE %v -> %v", i, prevBW, silkBW)
		}
		prevBW = silkBW
		prevBWSet = true

		// Get sMid before decode
		sMidBefore := silkDec.GetSMid()

		// Decode
		silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)

		// Get sMid after decode
		sMidAfter := silkDec.GetSMid()

		t.Logf("Packet %d: BW=%v, sMid before=[%d,%d], after=[%d,%d]",
			i, silkBW, sMidBefore[0], sMidBefore[1], sMidAfter[0], sMidAfter[1])
	}

	// Process packets 0-819 to fill libopus state (without logging)
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()
	for i := 0; i <= 819; i++ {
		libDec2.DecodeFloat(packets[i], 1920)
	}
	for i := 820; i <= 825; i++ {
		libDec2.DecodeFloat(packets[i], 1920)
	}

	// Now packet 826
	t.Log("\n=== PACKET 826 DETAILED TRACE ===")
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	t.Logf("TOC: Mode=%v, BW=%v, FrameSize=%d", toc.Mode, toc.Bandwidth, toc.FrameSize)
	t.Logf("Previous BW=%v, Current BW=%v", prevBW, silkBW)

	// Get state before decode
	sMidBefore := silkDec.GetSMid()
	t.Logf("sMid before decode: [%d, %d]", sMidBefore[0], sMidBefore[1])

	// Get resampler state before decode
	nbResampler := silkDec.GetResampler(silk.BandwidthNarrowband)
	if nbResampler != nil {
		t.Logf("NB Resampler exists BEFORE decode")
		t.Logf("  sIIR: %v", nbResampler.GetSIIR())
		t.Logf("  sFIR: %v", nbResampler.GetSFIR())
	} else {
		t.Log("NB Resampler does NOT exist before decode")
	}

	// Decode native samples manually
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])

	// Check what handleBandwidthChange would do
	t.Logf("\nBandwidth change check: prevBW=%v, currentBW=%v, changed=%v",
		prevBW, silkBW, prevBW != silkBW)

	// Trigger bandwidth change handling
	changed := silkDec.HandleBandwidthChange(silkBW)
	t.Logf("HandleBandwidthChange returned: %v", changed)

	// Check NB resampler again
	nbResamplerAfterBWChange := silkDec.GetResampler(silk.BandwidthNarrowband)
	if nbResamplerAfterBWChange != nil {
		t.Logf("NB Resampler exists AFTER HandleBandwidthChange")
		t.Logf("  sIIR: %v", nbResamplerAfterBWChange.GetSIIR())
		t.Logf("  sFIR: %v", nbResamplerAfterBWChange.GetSFIR())
	} else {
		t.Log("NB Resampler still does NOT exist after HandleBandwidthChange")
	}

	// Decode frame
	nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}
	t.Logf("\nNative SILK samples: %d", len(nativeSamples))
	t.Logf("First 20 native samples:")
	for i := 0; i < 20 && i < len(nativeSamples); i++ {
		int16Val := int16(nativeSamples[i] * 32768.0)
		t.Logf("  [%2d] float=%.6f int16=%d", i, nativeSamples[i], int16Val)
	}

	// Build resampler input
	resamplerInput := silkDec.BuildMonoResamplerInput(nativeSamples)
	t.Logf("\nResampler input (first 20):")
	for i := 0; i < 20 && i < len(resamplerInput); i++ {
		int16Val := int16(resamplerInput[i] * 32768.0)
		t.Logf("  [%2d] float=%.6f int16=%d", i, resamplerInput[i], int16Val)
	}

	// Get resampler
	resampler := silkDec.GetResampler(silkBW)
	t.Logf("\nResampler state before Process:")
	t.Logf("  sIIR: %v", resampler.GetSIIR())
	t.Logf("  sFIR: %v", resampler.GetSFIR())
	t.Logf("  inputDelay: %d", resampler.InputDelay())

	// Process
	output := resampler.Process(resamplerInput)
	t.Logf("\n48kHz output (first 30):")
	for i := 0; i < 30 && i < len(output); i++ {
		t.Logf("  [%2d] %.9f", i, output[i])
	}

	// Compare with libopus
	libOut, libN := libDec2.DecodeFloat(pkt, 1920)
	t.Logf("\nLibopus 48kHz output (first 30):")
	for i := 0; i < 30 && i < libN; i++ {
		t.Logf("  [%2d] %.9f", i, libOut[i])
	}
}
