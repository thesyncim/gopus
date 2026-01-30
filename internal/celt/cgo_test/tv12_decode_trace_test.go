// Package cgo traces the Decode path to find where zeros come from
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12DecodeTrace traces through Decode() to find where zeros come from
func TestTV12DecodeTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create standalone SILK decoder (like TestTV12SMidTracking)
	silkDec := silk.NewDecoder()

	// Process packets 0-136 to build state
	for i := 0; i <= 136; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])

		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				silkDec.Decode(pkt[1:], silkBW, toc.FrameSize, true)
			}
		}
	}

	// Now trace packet 137 decode
	pkt := packets[137]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))

	t.Logf("=== Tracing packet 137 decode ===")
	t.Logf("TOC: Mode=%v, BW=%d, FrameSize=%d", toc.Mode, toc.Bandwidth, toc.FrameSize)
	t.Logf("silkBW=%v", silkBW)

	// Get sMid before decode
	sMidBefore := silkDec.GetSMid()
	t.Logf("sMid BEFORE decode: [%d, %d]", sMidBefore[0], sMidBefore[1])

	// Check resampler state
	mbRes := silkDec.GetResampler(silk.BandwidthMediumband)
	if mbRes != nil {
		sIIR := mbRes.GetSIIR()
		t.Logf("MB resampler sIIR BEFORE: [%d, %d, %d, %d, %d, %d]",
			sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])
	} else {
		t.Log("MB resampler: nil (will be created)")
	}

	// ============= MANUAL TRACE of Decode() =============
	t.Log("\n=== Manual trace of Decode() steps ===")

	// Step 1: handleBandwidthChange (should reset MB resampler)
	t.Log("Step 1: Calling handleBandwidthChange manually...")
	// This would reset the MB resampler

	// Step 2: DecodeFrame at native rate
	t.Log("Step 2: DecodeFrame at native rate...")
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}
	t.Logf("  Native samples: %d", len(nativeSamples))

	// Show first 10 native samples
	t.Log("  First 10 native samples:")
	for i := 0; i < 10 && i < len(nativeSamples); i++ {
		t.Logf("    [%2d] %.6f (int16: %d)", i, nativeSamples[i], int16(nativeSamples[i]*32768))
	}

	// Step 3: Get resampler
	t.Log("\nStep 3: Getting MB resampler...")
	resampler := silkDec.GetResampler(silkBW)
	sIIR := resampler.GetSIIR()
	t.Logf("  sIIR: [%d, %d, %d, %d, %d, %d]", sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])

	// Step 4: BuildMonoResamplerInput
	t.Log("\nStep 4: BuildMonoResamplerInput...")
	config := silk.GetBandwidthConfig(silkBW)
	t.Logf("  Config: SampleRate=%d", config.SampleRate)

	// Calculate frame params
	fsKHz := config.SampleRate / 1000
	frameLength := len(nativeSamples) // for 20ms, should be 240 for MB
	t.Logf("  fsKHz=%d, frameLength=%d", fsKHz, frameLength)

	// Build resampler input manually
	sMidNow := silkDec.GetSMid()
	t.Logf("  sMid for input: [%d, %d]", sMidNow[0], sMidNow[1])

	resamplerInput := make([]float32, frameLength)
	resamplerInput[0] = float32(sMidNow[1]) / 32768.0
	copy(resamplerInput[1:], nativeSamples[:len(nativeSamples)-1])

	t.Log("  First 10 resamplerInput:")
	for i := 0; i < 10 && i < len(resamplerInput); i++ {
		t.Logf("    [%2d] %.6f", i, resamplerInput[i])
	}

	// Step 5: Process through resampler
	t.Log("\nStep 5: resampler.Process()...")
	processedOutput := resampler.Process(resamplerInput)
	t.Logf("  Output length: %d", len(processedOutput))

	// Count non-zero
	nonZero := 0
	for _, v := range processedOutput {
		if v != 0 {
			nonZero++
		}
	}
	t.Logf("  Non-zero samples: %d / %d", nonZero, len(processedOutput))

	t.Log("  First 20 processed output:")
	for i := 0; i < 20 && i < len(processedOutput); i++ {
		t.Logf("    [%2d] %.6f", i, processedOutput[i])
	}

	// ============= NOW CALL ACTUAL Decode() =============
	t.Log("\n=== Now calling actual silkDec.Decode() ===")

	// Reset the decoder state to before packet 137
	silkDec2 := silk.NewDecoder()
	for i := 0; i <= 136; i++ {
		pkt := packets[i]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode == gopus.ModeSILK {
			silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
			if ok {
				silkDec2.Decode(pkt[1:], silkBW, toc.FrameSize, true)
			}
		}
	}

	// Call actual Decode
	output, err := silkDec2.Decode(pkt[1:], silkBW, toc.FrameSize, true)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	t.Logf("Actual output length: %d", len(output))

	nonZero = 0
	for _, v := range output {
		if v != 0 {
			nonZero++
		}
	}
	t.Logf("Actual non-zero samples: %d / %d", nonZero, len(output))

	t.Log("Actual first 20 output samples:")
	for i := 0; i < 20 && i < len(output); i++ {
		t.Logf("  [%2d] %.6f", i, output[i])
	}
}

// TestTV12DecodeTraceWithGopus traces the gopus.Decoder path for comparison
func TestTV12DecodeTraceWithGopus(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 145)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create gopus decoder (like TestTV12ResamplerState137)
	goDec, _ := gopus.NewDecoder(48000, 1)
	silkDec := goDec.GetSILKDecoder()

	// Process packets 0-136 to build state
	for i := 0; i < 137; i++ {
		goDec.DecodeFloat32(packets[i])
	}

	pkt := packets[137]
	toc := gopus.ParseTOC(pkt[0])

	t.Logf("=== Tracing packet 137 via gopus.Decoder ===")
	t.Logf("TOC: Mode=%v, BW=%d, FrameSize=%d", toc.Mode, toc.Bandwidth, toc.FrameSize)

	// Get sMid before decode
	sMidBefore := silkDec.GetSMid()
	t.Logf("sMid BEFORE decode: [%d, %d]", sMidBefore[0], sMidBefore[1])

	// Check MB resampler state
	mbRes := silkDec.GetResampler(silk.BandwidthMediumband)
	if mbRes != nil {
		sIIR := mbRes.GetSIIR()
		t.Logf("MB resampler sIIR: [%d, %d, %d, %d, %d, %d]",
			sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])
	} else {
		t.Log("MB resampler: nil (will be created)")
	}

	// Call DecodeFloat32
	output, err := goDec.DecodeFloat32(pkt)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	t.Logf("Output length: %d", len(output))

	// Get sMid after decode
	sMidAfter := silkDec.GetSMid()
	t.Logf("sMid AFTER decode: [%d, %d]", sMidAfter[0], sMidAfter[1])

	// Check if output is all zeros
	nonZeroCount := 0
	for _, v := range output {
		if v != 0 {
			nonZeroCount++
		}
	}
	t.Logf("Non-zero samples: %d / %d", nonZeroCount, len(output))

	// Show first 20 samples
	t.Log("\nFirst 20 output samples:")
	for i := 0; i < 20 && i < len(output); i++ {
		t.Logf("  [%2d] %.6f", i, output[i])
	}

	// Check resampler state AFTER decode
	mbRes = silkDec.GetResampler(silk.BandwidthMediumband)
	if mbRes != nil {
		sIIR := mbRes.GetSIIR()
		t.Logf("\nMB resampler sIIR AFTER: [%d, %d, %d, %d, %d, %d]",
			sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])
	}
}
