//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides tests to compare spread decision VALUES between gopus and libopus.
// Agent 22: Debug spread decision value divergence
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

// TestSpreadDecisionValue compares the computed spread decision between gopus and libopus
func TestSpreadDecisionValue(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a 440Hz sine wave
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*440.0*float64(i)/48000.0)
	}

	// Create gopus encoder
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)
	encoder.SetComplexity(10)

	// Encode a frame to trigger spread decision
	gopusBytes, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Create libopus encoder and encode
	pcm32 := make([]float32, frameSize)
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)

	libBytes, n := libEnc.EncodeFloat(pcm32, frameSize)
	if n < 0 {
		t.Fatalf("libopus encode failed: %d", n)
	}

	// Compare payloads
	libPayload := libBytes[1:]
	gopusPayload := gopusBytes

	t.Logf("gopus payload:   %d bytes", len(gopusPayload))
	t.Logf("libopus payload: %d bytes", len(libPayload))

	// Decode the spread decision from both payloads
	// The spread decision is encoded after TF encoding.
	// For a typical 440Hz sine first frame with transient=1, the bit position should be:
	// - Flags: ~4 bits
	// - Coarse energy: ~50-60 bits
	// - TF encoding: ~5 bits (all zeros with tf_select bit)
	// So spread should start around bit 60-65

	// Find divergence
	divergeIdx := -1
	for i := 0; i < minInt(len(gopusPayload), len(libPayload)); i++ {
		if gopusPayload[i] != libPayload[i] {
			divergeIdx = i
			break
		}
	}

	if divergeIdx >= 0 {
		t.Logf("First divergence at byte %d", divergeIdx)
		t.Logf("  gopus:   0x%02X (binary: %08b)", gopusPayload[divergeIdx], gopusPayload[divergeIdx])
		t.Logf("  libopus: 0x%02X (binary: %08b)", libPayload[divergeIdx], libPayload[divergeIdx])

		// Show more context
		start := maxInt(0, divergeIdx-4)
		end := minInt(len(gopusPayload), divergeIdx+8)
		endLib := minInt(len(libPayload), divergeIdx+8)
		if endLib < end {
			end = endLib
		}

		t.Log("\nByte comparison around divergence:")
		for i := start; i < end; i++ {
			gByte := gopusPayload[i]
			lByte := byte(0)
			if i < len(libPayload) {
				lByte = libPayload[i]
			}
			marker := ""
			if i == divergeIdx {
				marker = " <-- DIVERGE"
			} else if gByte != lByte {
				marker = " <-- mismatch"
			}
			t.Logf("  [%d] gopus=%02X (%08b) libopus=%02X (%08b)%s",
				i, gByte, gByte, lByte, lByte, marker)
		}
	} else {
		t.Log("EXACT MATCH!")
	}
}

// TestDebugSpreadDecisionInputs inspects what inputs gopus uses for spread decision
func TestDebugSpreadDecisionInputs(t *testing.T) {
	frameSize := 960
	channels := 1
	nbBands := 21

	// Generate a 440Hz sine wave
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*440.0*float64(i)/48000.0)
	}

	// Process MDCT to get normalized coefficients
	// We need to replicate the encoding pipeline to get the normalized coefficients
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)
	encoder.SetComplexity(10)

	// First, let's look at what the spread decision is based on
	// The spread decision uses normalized MDCT coefficients
	t.Log("Spread decision analysis:")
	t.Log("  The spreading_decision() function analyzes normalized MDCT coefficients")
	t.Log("  It counts coefficients below certain thresholds to determine tonality")
	t.Log("  For a pure 440Hz tone, most energy is concentrated in a few bins")
	t.Log("  This should result in SPREAD_AGGRESSIVE (3) for tonal signals")
	t.Log("")

	// Encode to get actual result
	gopusBytes, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("EncodeFrame failed: %v", err)
	}

	t.Logf("Encoded %d bytes", len(gopusBytes))
	t.Logf("First 16 bytes: %02x", gopusBytes[:minInt(16, len(gopusBytes))])

	// Compute spread weights for analysis
	// We need the band energies
	mode := celt.GetModeConfig(frameSize)
	_ = mode
	_ = nbBands
}

