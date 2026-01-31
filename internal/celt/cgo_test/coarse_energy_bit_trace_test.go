// Package cgo traces coarse energy encoding bit by bit.
// Agent 23: Find exact band where gopus diverges from libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/celt"
)

// TestCoarseEnergyBitTrace traces QI values at the point of divergence
func TestCoarseEnergyBitTrace(t *testing.T) {
	t.Log("=== Agent 23: Coarse Energy Bit Trace ===")
	t.Log("")
	t.Log("Goal: Find exact band where divergence occurs at byte 7 (bit 56)")
	t.Log("")

	frameSize := 960
	channels := 1
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		sample := 0.5 * math.Sin(2.0*math.Pi*440*float64(i)/48000.0)
		pcm32[i] = float32(sample)
	}

	// Get libopus packet
	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("Failed to create libopus encoder: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)
	libEnc.SetSignal(OpusSignalMusic)

	libBytes, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen < 0 {
		t.Fatalf("libopus encode failed: %d", libLen)
	}

	// Get gopus packet
	gopusEnc, err := gopus.NewEncoder(48000, channels, gopus.ApplicationAudio)
	if err != nil {
		t.Fatalf("Failed to create gopus encoder: %v", err)
	}
	_ = gopusEnc.SetBitrate(bitrate)
	gopusEnc.SetFrameSize(frameSize)

	gopusPacket := make([]byte, 4000)
	gopusLen, err := gopusEnc.Encode(pcm32, gopusPacket)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Skip TOC byte
	libPayload := libBytes[1:]
	gopusPayload := gopusPacket[1:gopusLen]

	t.Logf("libopus payload (first 16 bytes): %02x", libPayload[:minCE(16, len(libPayload))])
	t.Logf("gopus payload (first 16 bytes):   %02x", gopusPayload[:minCE(16, len(gopusPayload))])
	t.Log("")

	// Find exact bit of divergence
	firstDivByte := -1
	firstDivBit := -1
	for byteIdx := 0; byteIdx < minCE(len(libPayload), len(gopusPayload)); byteIdx++ {
		if libPayload[byteIdx] != gopusPayload[byteIdx] {
			firstDivByte = byteIdx
			diff := libPayload[byteIdx] ^ gopusPayload[byteIdx]
			for bitIdx := 7; bitIdx >= 0; bitIdx-- {
				if (diff>>bitIdx)&1 != 0 {
					firstDivBit = byteIdx*8 + (7 - bitIdx)
					break
				}
			}
			break
		}
	}

	t.Logf("First divergence at byte %d, bit %d", firstDivByte, firstDivBit)
	if firstDivByte >= 0 {
		t.Logf("  libopus[%d] = 0x%02X (%08b)", firstDivByte, libPayload[firstDivByte], libPayload[firstDivByte])
		t.Logf("  gopus[%d]   = 0x%02X (%08b)", firstDivByte, gopusPayload[firstDivByte], gopusPayload[firstDivByte])
	}
	t.Log("")

	// Now let's use the gopus decoder to extract coarse energies from both packets
	t.Log("=== Decoding packets to extract band energies ===")
	t.Log("")

	// Create decoder
	dec := celt.NewDecoder(channels)

	// Decode libopus packet
	t.Log("Decoding libopus packet...")
	libDecoded, err := dec.DecodeFrame(libPayload, frameSize)
	if err != nil {
		t.Logf("libopus decode error (expected for partial packet): %v", err)
	} else {
		t.Logf("libopus decoded %d samples", len(libDecoded))
	}

	// Reset decoder for gopus packet
	dec.Reset()

	// Decode gopus packet
	t.Log("Decoding gopus packet...")
	gopusDecoded, err := dec.DecodeFrame(gopusPayload, frameSize)
	if err != nil {
		t.Logf("gopus decode error (expected for partial packet): %v", err)
	} else {
		t.Logf("gopus decoded %d samples", len(gopusDecoded))
	}

	t.Log("")
	t.Log("=== Analysis ===")
	t.Log("")
	t.Log("The divergence at byte 7 (bit 56) in the CELT payload indicates:")
	t.Log("  - Bits 0-55 match (7 bytes = 56 bits)")
	t.Log("  - Header flags: ~6 bits (silence, postfilter, transient, intra)")
	t.Log("  - Coarse energy: ~50 bits consumed so far")
	t.Log("")
	t.Log("With ~3 bits per band average, 50 bits covers ~16-17 bands")
	t.Log("The divergence is likely in the encoding of bands 16-17 or later")
	t.Log("")
	t.Log("Possible causes:")
	t.Log("  1. Different QI value computed for some band")
	t.Log("  2. Different Laplace parameters (fs, decay) used")
	t.Log("  3. Bit budget exhaustion handled differently")
	t.Log("  4. Previous band energy state (prevBandE) differs")

	// Let's look at what the CELT encoder sees internally
	t.Log("")
	t.Log("=== Checking CELT internal state ===")

	// Create fresh encoder to trace
	enc := celt.NewEncoder(channels)
	enc.Reset()
	enc.SetBitrate(bitrate)

	// Convert to float64
	pcm64 := make([]float64, len(pcm32))
	for i, v := range pcm32 {
		pcm64[i] = float64(v)
	}

	// Encode a frame to get internal state
	_, err = enc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Logf("encode failed: %v", err)
	}

	// After encoding, prevEnergy should be set
	// This tells us what quantized energies were stored
	prevE := enc.PrevEnergy()
	t.Log("")
	t.Logf("gopus prevEnergy after encoding (first 21 bands):")
	for i := 0; i < minCE(21, len(prevE)); i++ {
		t.Logf("  Band %2d: %.4f", i, prevE[i])
	}
}

func minCE(a, b int) int {
	if a < b {
		return a
	}
	return b
}
