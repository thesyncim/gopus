// Package cgo provides full frame encoding trace tests.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

// TestFullFramePayloadComparison compares gopus vs libopus payloads byte-by-byte.
func TestFullFramePayloadComparison(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
		pcm32[i] = float32(val)
	}

	t.Log("=== Full Frame Payload Comparison ===")
	t.Logf("Frame size: %d, Bitrate: %d", frameSize, bitrate)
	t.Log("")

	// Encode with gopus
	gopusEnc := encoder.NewEncoder(sampleRate, 1)
	gopusEnc.SetMode(encoder.ModeCELT)
	gopusEnc.SetBandwidth(types.BandwidthFullband)
	gopusEnc.SetBitrate(bitrate)
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
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	t.Logf("Gopus packet: %d bytes, Libopus packet: %d bytes", len(gopusPacket), len(libPacket))

	// Extract payloads (skip TOC byte)
	if len(gopusPacket) < 2 || len(libPacket) < 2 {
		t.Fatal("Packets too short")
	}

	gopusTOC := gopusPacket[0]
	libTOC := libPacket[0]
	t.Logf("TOC: gopus=0x%02X, libopus=0x%02X", gopusTOC, libTOC)

	gopusPayload := gopusPacket[1:]
	libPayload := libPacket[1:]

	// Find first divergence point
	minLen := len(gopusPayload)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	divergeIdx := -1
	for i := 0; i < minLen; i++ {
		if gopusPayload[i] != libPayload[i] {
			divergeIdx = i
			break
		}
	}

	if divergeIdx == -1 && len(gopusPayload) == len(libPayload) {
		t.Log("SUCCESS: Payloads are identical!")
		return
	}

	if divergeIdx == -1 {
		t.Logf("Payloads match up to length %d, but lengths differ", minLen)
		divergeIdx = minLen
	}

	t.Logf("DIVERGENCE at payload byte %d", divergeIdx)
	t.Log("")

	// Show matching bytes
	matchingBytes := divergeIdx
	if matchingBytes > 10 {
		matchingBytes = 10
	}
	t.Logf("Matching bytes [0:%d]:", matchingBytes)
	for i := 0; i < matchingBytes; i++ {
		t.Logf("  [%2d]: 0x%02X = 0x%02X (match)", i, gopusPayload[i], libPayload[i])
	}
	t.Log("")

	// Show divergence point and surrounding bytes
	t.Log("Divergence point and surrounding bytes:")
	startShow := divergeIdx - 2
	if startShow < 0 {
		startShow = 0
	}
	endShow := divergeIdx + 5
	if endShow > minLen {
		endShow = minLen
	}

	for i := startShow; i < endShow; i++ {
		gByte := gopusPayload[i]
		lByte := libPayload[i]
		marker := ""
		if gByte != lByte {
			marker = " <-- DIFFER"
		}
		t.Logf("  [%2d]: gopus=0x%02X (%08b), libopus=0x%02X (%08b)%s",
			i, gByte, gByte, lByte, lByte, marker)
	}
	t.Log("")

	// Binary comparison at divergence point
	if divergeIdx < minLen {
		gByte := gopusPayload[divergeIdx]
		lByte := libPayload[divergeIdx]
		xorVal := gByte ^ lByte

		t.Logf("Divergence analysis at byte %d:", divergeIdx)
		t.Logf("  gopus:  0x%02X = %08b", gByte, gByte)
		t.Logf("  libopus: 0x%02X = %08b", lByte, lByte)
		t.Logf("  XOR:     0x%02X = %08b", xorVal, xorVal)

		// Count differing bits
		diffBits := 0
		for b := 0; b < 8; b++ {
			if (xorVal>>b)&1 == 1 {
				diffBits++
			}
		}
		t.Logf("  %d bits differ", diffBits)
	}

	// Estimate what encoding step corresponds to divergence byte
	t.Log("")
	t.Log("Divergence byte analysis:")

	// Rough estimate of bits consumed per encoding stage
	// At 64kbps for 20ms frame: ~1280 bits total
	// Header flags: ~6 bits
	// Coarse energy: ~3-5 bits per band * 21 bands = ~63-105 bits = ~8-13 bytes
	// Fine energy: depends on allocation
	// PVQ: remaining bits

	estimatedBits := divergeIdx * 8
	t.Logf("  Divergence at byte %d = approximately bit %d", divergeIdx, estimatedBits)
	if estimatedBits < 10 {
		t.Log("  This is in HEADER encoding region")
	} else if estimatedBits < 150 {
		t.Log("  This is likely in COARSE ENERGY encoding region")
	} else if estimatedBits < 300 {
		t.Log("  This is likely in FINE ENERGY or TF encoding region")
	} else {
		t.Log("  This is likely in PVQ BAND encoding region")
	}
}

// TestFirstDivergingBand tries to identify which band's encoding diverges.
func TestFirstDivergingBand(t *testing.T) {
	t.Log("=== First Diverging Band Analysis ===")
	t.Log("")
	t.Log("Encoding header + coarse energy for bands 0-20")
	t.Log("and comparing each band's contribution to the bitstream.")
	t.Log("")

	// This would require tracking tell() after each Laplace encode
	// and comparing between gopus and libopus.

	// For now, let's verify that standalone Laplace for multiple qi values works
	t.Log("Testing Laplace encoding for qi values -5 to +5:")
	for qi := -5; qi <= 5; qi++ {
		// Use band 0 parameters
		fs := 72 << 7     // 9216
		decay := 127 << 6 // 8128

		libBytes, libVal, err := EncodeLaplace(qi, fs, decay)
		if err != nil {
			t.Fatalf("libopus EncodeLaplace failed for qi=%d: %v", qi, err)
		}

		t.Logf("  qi=%2d: libopus returned val=%2d, bytes=%X", qi, libVal, libBytes)
	}
}
