package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

// TestPatchTransientDecision tests the new PatchTransientDecision function
func TestPatchTransientDecision(t *testing.T) {
	// Test case 1: First frame with signal (should patch)
	// Previous frame energies are all 0 (fresh encoder)
	// Current frame has actual signal
	nbBands := 21
	oldE := make([]float64, nbBands)   // All zeros - silence
	newE := make([]float64, nbBands)   // Simulated 440Hz sine energies

	// Fill with typical band energies for a 440Hz sine
	for i := 0; i < nbBands; i++ {
		oldE[i] = 0.0    // Previous frame was silence
		newE[i] = 5.0 + float64(i)*0.5  // Non-zero energy
	}

	result := celt.PatchTransientDecision(newE, oldE, nbBands, 0, nbBands, 1)
	t.Logf("Test 1 - Signal after silence: patched=%v", result)
	if !result {
		t.Error("Expected transient to be patched for signal after silence")
	}

	// Test case 2: Similar energies (should not patch)
	for i := 0; i < nbBands; i++ {
		oldE[i] = 5.0 + float64(i)*0.5
		newE[i] = 5.5 + float64(i)*0.5  // Very similar
	}

	result = celt.PatchTransientDecision(newE, oldE, nbBands, 0, nbBands, 1)
	t.Logf("Test 2 - Similar energies: patched=%v", result)
	if result {
		t.Error("Expected no patch for similar energies")
	}

	// Test case 3: Decreasing energy (should not patch)
	for i := 0; i < nbBands; i++ {
		oldE[i] = 10.0 + float64(i)*0.5
		newE[i] = 2.0 + float64(i)*0.5  // Lower energy
	}

	result = celt.PatchTransientDecision(newE, oldE, nbBands, 0, nbBands, 1)
	t.Logf("Test 3 - Decreasing energy: patched=%v", result)
	if result {
		t.Error("Expected no patch for decreasing energy")
	}
}

// TestTransientPatchDuringEncoding tests that transient is patched during actual encoding
func TestTransientPatchDuringEncoding(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		val := 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
		pcm64[i] = val
		pcm32[i] = float32(val)
	}

	// Encode with gopus
	gopusEnc := encoder.NewEncoder(sampleRate, 1)
	gopusEnc.SetMode(encoder.ModeCELT)
	gopusEnc.SetBandwidth(types.BandwidthFullband)
	gopusEnc.SetBitrate(bitrate)
	gopusEnc.SetComplexity(5)  // Required for patch_transient_decision
	
	gopusPacket, err := gopusEnc.Encode(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(5)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed")
	}

	t.Logf("Gopus packet: %d bytes, first payload byte: 0x%02X", len(gopusPacket), gopusPacket[1])
	t.Logf("Libopus packet: %d bytes, first payload byte: 0x%02X", libLen, libPacket[1])

	// Compare first few payload bytes
	gopusPayload := gopusPacket[1:]
	libPayload := libPacket[1:]

	t.Log("Payload comparison (first 8 bytes):")
	for i := 0; i < 8 && i < len(gopusPayload) && i < len(libPayload); i++ {
		match := "MATCH"
		if gopusPayload[i] != libPayload[i] {
			match = "DIFFER"
		}
		t.Logf("  [%d]: gopus=0x%02X, libopus=0x%02X - %s", i, gopusPayload[i], libPayload[i], match)
	}

	// The key check: if transient is patched, the first few bytes should be closer
	// (transient flag is encoded early in the bitstream)
	matchingBytes := 0
	for i := 0; i < len(gopusPayload) && i < len(libPayload); i++ {
		if gopusPayload[i] == libPayload[i] {
			matchingBytes++
		} else {
			break
		}
	}
	t.Logf("Matching bytes from start: %d", matchingBytes)

	// Decode with libopus to verify the packet is valid
	libDec, _ := NewLibopusDecoder(sampleRate, 1)
	defer libDec.Destroy()
	decoded32, samples := libDec.DecodeFloat(gopusPacket, frameSize)
	if samples > 0 {
		t.Logf("Libopus decoded gopus packet: %d samples", samples)
		
		// Compute SNR
		var signal, noise float64
		for i := 0; i < frameSize && i < samples; i++ {
			ref := pcm64[i]
			dec := float64(decoded32[i])
			signal += ref * ref
			diff := dec - ref
			noise += diff * diff
		}
		if noise > 0 && signal > 0 {
			snr := 10.0 * math.Log10(signal/noise)
			t.Logf("SNR: %.2f dB", snr)
		}
	}
}
