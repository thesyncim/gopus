// Package cgo tests with long blocks (non-transient signal).
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestLongBlockComparison uses a steady signal to force long blocks.
func TestLongBlockComparison(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate a very smooth signal (low amplitude to avoid transient detection)
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		// Low frequency, low amplitude - should not trigger transient
		val := 0.1 * math.Sin(2*math.Pi*100*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
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
	libEnc.SetVBR(false)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)

	t.Logf("gopus packet: %d bytes", len(goPacket))
	t.Logf("libopus payload: %d bytes", len(libPayload))

	// Find first divergence
	minLen := len(goPacket)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	firstDiff := -1
	for i := 0; i < minLen; i++ {
		if goPacket[i] != libPayload[i] {
			firstDiff = i
			break
		}
	}

	if firstDiff < 0 {
		t.Log("Packets match completely!")
		return
	}

	t.Logf("First divergence at byte %d (bit %d)", firstDiff, firstDiff*8)
	t.Logf("gopus byte:  0x%02x = %08b", goPacket[firstDiff], goPacket[firstDiff])
	t.Logf("libopus byte: 0x%02x = %08b", libPayload[firstDiff], libPayload[firstDiff])
	t.Logf("XOR: 0x%02x", goPacket[firstDiff]^libPayload[firstDiff])

	t.Logf("Final range: gopus=0x%08X libopus=0x%08X", goEnc.FinalRange(), libEnc.GetFinalRange())
}

// TestSecondFrameComparison encodes a second frame to see if state issues exist.
func TestSecondFrameComparison(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
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
	libEnc.SetVBR(false)

	// Encode frame 1
	libPacket1, _ := libEnc.EncodeFloat(pcm32, frameSize)
	// Encode frame 2 (same input)
	libPacket2, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload2 := libPacket2[1:]

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	// Encode frame 1
	_, _ = goEnc.EncodeFrame(pcm64, frameSize)
	// Encode frame 2
	goPacket2, _ := goEnc.EncodeFrame(pcm64, frameSize)

	t.Log("=== Second Frame Comparison ===")
	t.Logf("libopus frame 1: %d bytes", len(libPacket1))
	t.Logf("gopus frame 2: %d bytes", len(goPacket2))
	t.Logf("libopus frame 2: %d bytes", len(libPayload2))

	// Find first divergence
	minLen := len(goPacket2)
	if len(libPayload2) < minLen {
		minLen = len(libPayload2)
	}

	firstDiff := -1
	for i := 0; i < minLen; i++ {
		if goPacket2[i] != libPayload2[i] {
			firstDiff = i
			break
		}
	}

	if firstDiff < 0 {
		t.Log("Second frame packets match completely!")
		return
	}

	t.Logf("First divergence at byte %d (bit %d)", firstDiff, firstDiff*8)
	t.Logf("gopus byte:  0x%02x", goPacket2[firstDiff])
	t.Logf("libopus byte: 0x%02x", libPayload2[firstDiff])

	t.Logf("Final range: gopus=0x%08X libopus=0x%08X", goEnc.FinalRange(), libEnc.GetFinalRange())
}
