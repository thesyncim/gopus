// Package cgo traces the resampler processing for packet 826.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12Packet826ResamplerTrace traces the resampler with debug output.
func TestTV12Packet826ResamplerTrace(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 830)
	if err != nil {
		t.Skip("Could not load packets")
	}

	// Create a FRESH SILK decoder (no state from previous packets)
	silkDec := silk.NewDecoder()

	// Only process packets 820-826 to minimize state
	t.Log("Processing packets 820-825 to build MB state...")
	for i := 820; i <= 825; i++ {
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

	// Get sMid values
	sMid := silkDec.GetSMid()
	t.Logf("sMid after packet 825: [%d, %d]", sMid[0], sMid[1])

	// Now decode packet 826 manually step by step
	pkt := packets[826]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	t.Logf("\n=== Packet 826 (NB 8kHz) ===")

	// Decode at native rate
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	nativeSamples, err := silkDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("DecodeFrame error: %v", err)
	}

	t.Logf("Native samples: %d", len(nativeSamples))
	t.Logf("First 10 native samples (float):")
	for i := 0; i < 10 && i < len(nativeSamples); i++ {
		// Convert to int16 like the resampler does
		scaled := nativeSamples[i] * 32768.0
		int16Val := int16(scaled)
		t.Logf("  [%d] float=%.6f int16=%d", i, nativeSamples[i], int16Val)
	}

	// Build resampler input
	resamplerInput := silkDec.BuildMonoResamplerInput(nativeSamples)
	t.Logf("\nResampler input (first 10):")
	for i := 0; i < 10 && i < len(resamplerInput); i++ {
		scaled := resamplerInput[i] * 32768.0
		int16Val := int16(scaled)
		t.Logf("  [%d] float=%.6f int16=%d", i, resamplerInput[i], int16Val)
	}

	// Create a FRESH NB resampler
	freshResampler := silk.NewLibopusResampler(8000, 48000)
	t.Logf("\nFresh NB resampler:")
	t.Logf("  inputDelay: %d", freshResampler.InputDelay())
	t.Logf("  sIIR: %v", freshResampler.GetSIIR())
	t.Logf("  sFIR: %v", freshResampler.GetSFIR())

	// Process and get output
	output := freshResampler.Process(resamplerInput)
	t.Logf("\nOutput from fresh resampler (first 30):")
	for i := 0; i < 30 && i < len(output); i++ {
		int16Val := int16(output[i] * 32768.0)
		t.Logf("  [%d] float=%.9f int16=%d", i, output[i], int16Val)
	}

	// Check final state
	t.Logf("\nFresh resampler state AFTER process:")
	t.Logf("  sIIR: %v", freshResampler.GetSIIR())
	t.Logf("  sFIR: %v", freshResampler.GetSFIR())

	// Now compare with libopus resampler directly
	t.Log("\n=== Direct libopus resampler comparison ===")

	// Convert resamplerInput to int16 array
	int16Input := make([]int16, len(resamplerInput))
	for i, v := range resamplerInput {
		scaled := v * 32768.0
		if scaled > 32767 {
			int16Input[i] = 32767
		} else if scaled < -32768 {
			int16Input[i] = -32768
		} else {
			int16Input[i] = int16(scaled)
		}
	}
	t.Logf("Int16 input (first 10): %v", int16Input[:10])

	// Call libopus resampler
	libOutput := ProcessLibopusResampler(int16Input, 8000, 48000)
	if libOutput != nil {
		t.Logf("\nLibopus resampler output (first 30):")
		for i := 0; i < 30 && i < len(libOutput); i++ {
			floatVal := float32(libOutput[i]) / 32768.0
			t.Logf("  [%d] int16=%d float=%.9f", i, libOutput[i], floatVal)
		}
	} else {
		t.Log("Libopus resampler not available")
	}
}
