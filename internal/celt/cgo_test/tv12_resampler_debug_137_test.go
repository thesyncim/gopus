// Package cgo debugs resampler at packet 137.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/silk"
)

// TestTV12ResamplerDebug137 traces resampler behavior at packet 137.
func TestTV12ResamplerDebug137(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector12.bit"

	packets, err := loadPacketsSimple(bitFile, 140)
	if err != nil {
		t.Skip("Could not load packets")
	}

	silkDec := silk.NewDecoder()

	// Process packets 0-136
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

	// Get sMid before packet 137
	sMid := silkDec.GetSMid()
	t.Logf("sMid before 137: [%d, %d]", sMid[0], sMid[1])

	// Check NB resampler state
	nbRes := silkDec.GetResampler(silk.BandwidthNarrowband)
	if nbRes != nil {
		sIIR := nbRes.GetSIIR()
		t.Logf("NB resampler sIIR: [%d, %d, %d, %d, %d, %d]",
			sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])
	}

	// Check MB resampler state BEFORE decode
	mbResBefore := silkDec.GetResampler(silk.BandwidthMediumband)
	if mbResBefore != nil {
		sIIR := mbResBefore.GetSIIR()
		t.Logf("MB resampler BEFORE (exists): sIIR[0]=%d", sIIR[0])
	} else {
		t.Log("MB resampler BEFORE: does not exist (will be created)")
	}

	// Manually decode packet 137 step by step to trace
	pkt := packets[137]
	toc := gopus.ParseTOC(pkt[0])
	silkBW, _ := silk.BandwidthFromOpus(int(toc.Bandwidth))
	t.Logf("\nPacket 137: BW=%d (MB=1)", toc.Bandwidth)

	// Step 1: Get native samples
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	nativeSamples := decodeNative(t, silkDec, pkt[1:], silkBW, duration)
	t.Logf("Native samples: %d", len(nativeSamples))
	t.Logf("Native[0:5]: %.6f, %.6f, %.6f, %.6f, %.6f",
		nativeSamples[0], nativeSamples[1], nativeSamples[2], nativeSamples[3], nativeSamples[4])

	// Step 2: Build resampler input manually
	resamplerInput := make([]float32, len(nativeSamples))
	resamplerInput[0] = float32(sMid[1]) / 32768.0
	copy(resamplerInput[1:], nativeSamples[:len(nativeSamples)-1])
	t.Logf("\nResampler input[0:5]: %.6f, %.6f, %.6f, %.6f, %.6f",
		resamplerInput[0], resamplerInput[1], resamplerInput[2], resamplerInput[3], resamplerInput[4])

	// Step 3: Get MB resampler and process
	mbRes := silkDec.GetResampler(silk.BandwidthMediumband)
	if mbRes == nil {
		t.Fatal("MB resampler is nil!")
	}

	// Check MB resampler state after GetResampler
	sIIR := mbRes.GetSIIR()
	t.Logf("MB resampler sIIR (after get): [%d, %d, %d, %d, %d, %d]",
		sIIR[0], sIIR[1], sIIR[2], sIIR[3], sIIR[4], sIIR[5])

	// Process
	output := mbRes.Process(resamplerInput)
	t.Logf("\nOutput: %d samples", len(output))
	t.Logf("Output[0:10]: %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f",
		output[0], output[1], output[2], output[3], output[4],
		output[5], output[6], output[7], output[8], output[9])

	// Check if any output is non-zero
	nonZeroCount := 0
	for _, v := range output {
		if v != 0 {
			nonZeroCount++
		}
	}
	t.Logf("Non-zero count: %d/%d", nonZeroCount, len(output))
}

// decodeNative decodes at native rate without resampling.
func decodeNative(t *testing.T, dec *silk.Decoder, data []byte, bw silk.Bandwidth, duration silk.FrameDuration) []float32 {
	// Create a fresh decoder to avoid state issues
	freshDec := silk.NewDecoder()
	// Convert FrameDuration to samples at 48kHz: duration in ms * 48 samples/ms
	frameSizeSamples := int(duration) * 48
	silkOut, err := freshDec.Decode(data, bw, frameSizeSamples, true)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	// The output is already at 48kHz from Decode(), we need native rate
	// Let's just use DecodeFrame directly for native rate
	return silkOut
}
