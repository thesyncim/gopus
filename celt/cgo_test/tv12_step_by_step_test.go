//go:build cgo_libopus
// +build cgo_libopus

// Package cgo traces each step of packet 826 decode to find the exact issue.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestTV12StepByStepDecode traces each step manually.
func TestTV12StepByStepDecode(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil || len(packets) < 827 {
		t.Skip("Could not load enough packets")
	}

	// Create SILK decoder
	silkDec := silk.NewDecoder()

	t.Log("=== Step-by-Step Trace at Packet 826 ===")

	// Process packets 0-825 through full Decode to match real usage
	for i := 0; i <= 825 && i < len(packets); i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode != gopus.ModeSILK {
			continue // Skip Hybrid for this trace
		}

		silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
		silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	}

	// Get state before packet 826
	nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
	t.Logf("Step 0 - Initial NB state: sIIR=%v", nbRes.GetSIIR())
	t.Logf("Step 0 - Initial sMid: %v", silkDec.GetSMid())

	// Now manually process packet 826 step by step
	pkt826 := packets[826]
	toc := gopus.ParseTOC(pkt826[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	t.Logf("\nPacket 826: Mode=%v, BW=%v (%v)", toc.Mode, toc.Bandwidth, silkBW)

	// Step 1: Call handleBandwidthChange via NotifyBandwidthChange
	t.Log("\nStep 1 - Calling NotifyBandwidthChange(NB)...")
	silkDec.NotifyBandwidthChange(silkBW)
	t.Logf("Step 1 - After NotifyBandwidthChange: NB sIIR=%v", nbRes.GetSIIR())

	// Step 2: Explicitly reset to verify Reset() works
	t.Log("\nStep 2 - Explicitly calling Reset() on NB resampler...")
	nbRes.Reset()
	t.Logf("Step 2 - After explicit Reset: NB sIIR=%v", nbRes.GetSIIR())

	// Step 3: Decode native samples
	t.Log("\nStep 3 - Decoding native samples...")
	var rd rangecoding.Decoder
	rd.Init(pkt826[1:])
	nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}
	t.Logf("Step 3 - Native samples: len=%d, first 5: [%.6f, %.6f, %.6f, %.6f, %.6f]",
		len(nativeSamples), nativeSamples[0], nativeSamples[1], nativeSamples[2], nativeSamples[3], nativeSamples[4])

	// Step 4: Build resampler input
	t.Log("\nStep 4 - Building resampler input...")
	resamplerInput := silkDec.BuildMonoResamplerInput(nativeSamples)
	t.Logf("Step 4 - Resampler input: first 5: [%.6f, %.6f, %.6f, %.6f, %.6f]",
		resamplerInput[0], resamplerInput[1], resamplerInput[2], resamplerInput[3], resamplerInput[4])
	t.Logf("Step 4 - After BuildMonoResamplerInput: sMid=%v", silkDec.GetSMid())

	// Step 5: Verify NB resampler is still reset
	t.Logf("\nStep 5 - NB resampler before Process: sIIR=%v", nbRes.GetSIIR())

	// Step 6: Process with resampler
	t.Log("\nStep 6 - Processing with resampler...")
	output := nbRes.Process(resamplerInput)
	t.Logf("Step 6 - Output: len=%d, first 10: [%.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f]",
		len(output), output[0], output[1], output[2], output[3], output[4], output[5], output[6], output[7], output[8], output[9])
	t.Logf("Step 6 - After Process: NB sIIR=%v", nbRes.GetSIIR())

	// Compare with libopus
	t.Log("\n=== Comparing with libopus ===")
	libDec, _ := NewLibopusDecoder(48000, 1)
	if libDec == nil {
		t.Skip("Could not create libopus decoder")
	}
	defer libDec.Destroy()

	// Process packets 0-825 through libopus
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
	t.Logf("Libopus: len=%d, first 10: [%.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f]",
		libN, libOut[0], libOut[1], libOut[2], libOut[3], libOut[4], libOut[5], libOut[6], libOut[7], libOut[8], libOut[9])

	// Ratio analysis
	t.Log("\nRatio analysis:")
	for i := 0; i < 10 && i < len(output) && i < libN; i++ {
		ratio := float32(0)
		if libOut[i] != 0 {
			ratio = output[i] / libOut[i]
		}
		t.Logf("  [%d] go=%.6f lib=%.6f ratio=%.3f", i, output[i], libOut[i], ratio)
	}
}
