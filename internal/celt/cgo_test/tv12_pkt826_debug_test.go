// Package cgo debugs packet 826 resampler input.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Packet826Debug traces the decode pipeline for packet 826.
func TestTV12Packet826Debug(t *testing.T) {
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

		// Decode with full pipeline
		_, err := silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
		if err != nil {
			t.Logf("Packet %d: decode error: %v", i, err)
		}

		if i == 825 {
			t.Logf("Packet 825 bandwidth: %v", silkBW)
		}
	}

	// Now trace packet 826 in detail
	t.Log("\n=== Tracing packet 826 ===")

	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	t.Logf("TOC: Mode=%v, Bandwidth=%v, FrameSize=%d", toc.Mode, toc.Bandwidth, toc.FrameSize)

	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	bwName := []string{"NB 8kHz", "MB 12kHz", "WB 16kHz"}[silkBW]
	t.Logf("SILK bandwidth: %v (%s)", silkBW, bwName)

	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	t.Logf("Duration: %v", duration)

	// Get sMid before any processing
	sMidBefore := silkDec.GetSMid()
	t.Logf("sMid BEFORE decode: [%d, %d] (float: [%.6f, %.6f])",
		sMidBefore[0], sMidBefore[1],
		float32(sMidBefore[0])/32768.0, float32(sMidBefore[1])/32768.0)

	// Get resampler BEFORE decode
	resamplerBefore := silkDec.GetResampler(silkBW)
	if resamplerBefore != nil {
		t.Logf("NB Resampler state BEFORE:")
		t.Logf("  inputDelay: %d", resamplerBefore.InputDelay())
		t.Logf("  sIIR: %v", resamplerBefore.GetSIIR())
		t.Logf("  sFIR: %v", resamplerBefore.GetSFIR())
		delayBuf := resamplerBefore.GetDelayBuf()
		t.Logf("  delayBuf[0:8]: %v", delayBuf[:minInt(8, len(delayBuf))])
	} else {
		t.Log("NB Resampler: nil (will be created)")
	}

	// Manually do what Decode does:

	// 1. handleBandwidthChange
	silkDec.HandleBandwidthChange(silkBW)
	t.Logf("\nAfter HandleBandwidthChange:")

	// 2. DecodeFrame - decode at native rate
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}

	t.Logf("Native samples: %d", len(nativeSamples))
	t.Logf("First 10 native samples:")
	for i := 0; i < 10 && i < len(nativeSamples); i++ {
		t.Logf("  [%d] %.6f", i, nativeSamples[i])
	}
	t.Logf("Last 5 native samples:")
	for i := len(nativeSamples) - 5; i < len(nativeSamples); i++ {
		t.Logf("  [%d] %.6f", i, nativeSamples[i])
	}

	// 3. BuildMonoResamplerInput
	resamplerInput := silkDec.BuildMonoResamplerInput(nativeSamples)
	t.Logf("\nResampler input (length %d):", len(resamplerInput))
	t.Logf("First 10 resampler input samples:")
	for i := 0; i < 10 && i < len(resamplerInput); i++ {
		t.Logf("  [%d] %.6f", i, resamplerInput[i])
	}
	t.Logf("  [0] should be sMid[1] = %.6f", float32(sMidBefore[1])/32768.0)

	// 4. Get resampler and check state
	resampler := silkDec.GetResampler(silkBW)
	t.Logf("\nNB Resampler state BEFORE Process:")
	t.Logf("  inputDelay: %d", resampler.InputDelay())
	t.Logf("  sIIR: %v", resampler.GetSIIR())
	t.Logf("  sFIR: %v", resampler.GetSFIR())
	delayBuf := resampler.GetDelayBuf()
	t.Logf("  delayBuf: %v", delayBuf[:minInt(8, len(delayBuf))])

	// 5. Process
	output := resampler.Process(resamplerInput)
	t.Logf("\nResampler output (length %d):", len(output))
	t.Logf("First 20 output samples:")
	for i := 0; i < 20 && i < len(output); i++ {
		t.Logf("  [%d] %.6f", i, output[i])
	}

	t.Logf("\nResampler state AFTER Process:")
	t.Logf("  sIIR: %v", resampler.GetSIIR())
	t.Logf("  sFIR: %v", resampler.GetSFIR())
	delayBuf2 := resampler.GetDelayBuf()
	t.Logf("  delayBuf: %v", delayBuf2[:minInt(8, len(delayBuf2))])
}
