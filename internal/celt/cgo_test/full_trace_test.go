//go:build trace
// +build trace

// Package cgo provides full encoding trace tests.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestFullEncodingTraceVsLibopus traces the full encoding to find divergence.
func TestFullEncodingTraceVsLibopus(t *testing.T) {
	frameSize := 960
	bitrate := 64000

	// Generate 440Hz sine wave
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000.0)
	}
	samples32 := make([]float32, frameSize)
	for i, v := range samples {
		samples32[i] = float32(v)
	}

	t.Log("=== Full Encoding Trace vs Libopus ===")
	t.Log("")

	// Encode with gopus (CBR)
	gopusEnc := celt.NewEncoder(1)
	gopusEnc.Reset()
	gopusEnc.SetBitrate(bitrate)
	gopusEnc.SetVBR(false)

	gopusPacket, err := gopusEnc.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}
	t.Logf("gopus: %d bytes", len(gopusPacket))

	// Encode with libopus (CBR)
	libEnc, err := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)
	libEnc.SetSignal(OpusSignalMusic)

	libPacket, _ := libEnc.EncodeFloat(samples32, frameSize)
	libPayload := libPacket[1:] // skip TOC
	t.Logf("libopus: %d bytes (payload)", len(libPayload))

	// Find first divergence
	minLen := len(gopusPacket)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	divergeAt := -1
	for i := 0; i < minLen; i++ {
		if gopusPacket[i] != libPayload[i] {
			divergeAt = i
			break
		}
	}

	if divergeAt < 0 {
		t.Log("Packets are IDENTICAL!")
		return
	}

	t.Logf("\nFirst divergence at byte %d", divergeAt)
	t.Log("")

	// Show bytes leading up to and including divergence
	start := 0
	end := divergeAt + 5
	if end > minLen {
		end = minLen
	}

	t.Log("Byte comparison:")
	for i := start; i < end; i++ {
		marker := ""
		if i == divergeAt {
			marker = " <-- FIRST DIFF"
		} else if gopusPacket[i] != libPayload[i] {
			marker = " <-- diff"
		}
		t.Logf("  [%3d] gopus=0x%02X libopus=0x%02X%s", i, gopusPacket[i], libPayload[i], marker)
	}

	// Binary analysis of divergent byte
	t.Log("")
	t.Logf("Binary analysis of byte %d:", divergeAt)
	gb := gopusPacket[divergeAt]
	lb := libPayload[divergeAt]
	t.Logf("  gopus:   0x%02X = 0b%08b", gb, gb)
	t.Logf("  libopus: 0x%02X = 0b%08b", lb, lb)
	t.Logf("  XOR:     0x%02X = 0b%08b", gb^lb, gb^lb)

	// Estimate what stage this is
	// Bit position = byte * 8 + bit_in_byte
	bitPos := divergeAt * 8
	t.Log("")
	t.Logf("Divergence at bit ~%d (byte %d)", bitPos, divergeAt)
	t.Log("")
	t.Log("Estimated position in encoding pipeline:")
	t.Log("  Bits 0-6:   Header flags (silence, postfilter, transient, intra)")
	t.Log("  Bits 7-85:  Coarse energy (~78 bits for 21 bands)")
	t.Log("  Bits 86-95: TF encoding (~10 bits)")
	t.Log("  Bits 96+:   Spread, dynalloc, trim, allocation, ...")

	if bitPos < 7 {
		t.Log(">>> Divergence in HEADER FLAGS")
	} else if bitPos < 86 {
		t.Log(">>> Divergence in COARSE ENERGY")
	} else if bitPos < 96 {
		t.Log(">>> Divergence in TF ENCODING")
	} else if bitPos < 100 {
		t.Log(">>> Divergence in SPREAD DECISION")
	} else {
		t.Log(">>> Divergence in ALLOCATION or later")
	}

	// Show final range comparison
	t.Log("")
	t.Logf("Final range: gopus=0x%08X, libopus=0x%08X", gopusEnc.FinalRange(), libEnc.GetFinalRange())
}

// TestAnalyzeByte11Divergence focuses on the byte 11 divergence.
func TestAnalyzeByte11Divergence(t *testing.T) {
	// Based on previous tests:
	// - Byte 11 diverges (gopus=0xE3, libopus=0xE1)
	// - XOR = 0x02 = bit 1 differs
	// - Bit 88-95 (byte 11) is in TF/spread territory

	t.Log("=== Byte 11 Divergence Analysis ===")
	t.Log("")
	t.Log("Observations from previous tests:")
	t.Log("  - First 11 bytes (88 bits) match perfectly")
	t.Log("  - Byte 11: gopus=0xE3 (11100011), libopus=0xE1 (11100001)")
	t.Log("  - Difference: bit 1 (value 2)")
	t.Log("")
	t.Log("Bit position analysis:")
	t.Log("  - Coarse energy uses ~78 bits (10 bytes)")
	t.Log("  - Header flags use ~6 bits")
	t.Log("  - So bit 88 should be around TF encoding")
	t.Log("")
	t.Log("Possible causes:")
	t.Log("  1. TF analysis produces different tfRes values")
	t.Log("  2. tfSelect differs between gopus and libopus")
	t.Log("  3. Different transient detection result")
	t.Log("  4. tf_estimate value differs")
	t.Log("")

	// Let's verify by encoding step by step
	frameSize := 960
	bitrate := 64000
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000.0)
	}

	// Get gopus encoding details
	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(bitrate)
	enc.SetVBR(false)

	// Encode and capture intermediate state
	_, err := enc.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	t.Logf("gopus final range: 0x%08X", enc.FinalRange())

	// Now check what TF parameters gopus used
	// We can't easily get this without modifying the encoder to expose them
	// Instead, let's trace through manually

	t.Log("")
	t.Log("To further debug, we need to:")
	t.Log("  1. Add tracing to gopus TF analysis")
	t.Log("  2. Compare tfRes array values with libopus")
	t.Log("  3. Compare tf_estimate values")
}
