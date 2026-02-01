//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests transient detection consistency between gopus and libopus.
package cgo

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus"
)

// TestToneishnessInEncoder tests that toneishness is correctly computed during encoding
func TestToneishnessInEncoder(t *testing.T) {
	frameSize := 960
	channels := 1
	sampleRate := 48000

	// Generate continuous 440 Hz sine (multiple frames worth)
	totalSamples := frameSize * 3
	pcm64 := make([]float64, totalSamples)
	for i := 0; i < totalSamples; i++ {
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	enc := celt.NewEncoder(channels)
	enc.Reset()

	overlap := celt.Overlap
	delayComp := 192

	// Encode 3 frames to see how toneishness changes
	for frame := 0; frame < 3; frame++ {
		framePCM := pcm64[frame*frameSize : (frame+1)*frameSize]

		// Apply same preprocessing as encoding
		dcRejected := enc.ApplyDCReject(framePCM)
		combinedBuf := make([]float64, delayComp+len(dcRejected))
		copy(combinedBuf[delayComp:], dcRejected)
		samplesForFrame := combinedBuf[:frameSize]
		preemph := enc.ApplyPreemphasisWithScaling(samplesForFrame)

		// Build transient input (overlap + preemph)
		transientInput := make([]float64, (overlap+frameSize)*channels)
		// Copy overlap buffer (has history for non-first frames)
		copy(transientInput[:overlap*channels], enc.OverlapBuffer()[:overlap*channels])
		copy(transientInput[overlap*channels:], preemph)

		result := enc.TransientAnalysis(transientInput, frameSize+overlap, false)

		t.Logf("Frame %d:", frame)
		t.Logf("  IsTransient: %v (MaskMetric: %.2f)", result.IsTransient, result.MaskMetric)
		t.Logf("  ToneFreq: %.4f rad/sample (%.1f Hz)", result.ToneFreq, result.ToneFreq*float64(sampleRate)/(2*math.Pi))
		t.Logf("  Toneishness: %.6f -> TF Analysis %s", result.Toneishness,
			map[bool]string{true: "ENABLED", false: "DISABLED"}[result.Toneishness < 0.98])

		// Update overlap buffer for next frame
		tailStart := len(preemph) - overlap*channels
		if tailStart >= 0 {
			copy(enc.OverlapBuffer()[:overlap*channels], preemph[tailStart:])
		}
	}
}

// TestByteByteComparison does detailed byte comparison to find exact divergence point.
func TestByteByteComparison(t *testing.T) {
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

	t.Log("=== Detailed Byte-by-Byte Comparison ===")

	// Encode with gopus
	gopusEnc := encoder.NewEncoder(sampleRate, 1)
	gopusEnc.SetMode(encoder.ModeCELT)
	gopusEnc.SetBandwidth(gopus.BandwidthFullband)
	gopusEnc.SetBitrate(bitrate)
	gopusPacket, err := gopusEnc.Encode(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Encode with libopus (matching settings)
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(5) // Match gopus default
	libEnc.SetBandwidth(OpusBandwidthFullband)

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed")
	}

	// Skip TOC byte
	gopusPayload := gopusPacket[1:]
	libPayload := libPacket[1:]

	t.Logf("Gopus payload length: %d", len(gopusPayload))
	t.Logf("Libopus payload length: %d", len(libPayload))
	t.Log("")

	// Show first 12 bytes with binary and hex
	t.Log("First 12 bytes comparison:")
	t.Log("Byte  Gopus(hex) Gopus(bin)     Libopus(hex) Libopus(bin)   Match?")
	t.Log("----  ---------- -----------    ------------ -----------    ------")

	for i := 0; i < 12; i++ {
		var gByte, lByte byte
		var gStr, lStr string

		if i < len(gopusPayload) {
			gByte = gopusPayload[i]
			gStr = fmt.Sprintf("0x%02X       %08b", gByte, gByte)
		} else {
			gStr = "--         --------"
		}

		if i < len(libPayload) {
			lByte = libPayload[i]
			lStr = fmt.Sprintf("0x%02X         %08b", lByte, lByte)
		} else {
			lStr = "--           --------"
		}

		match := "YES"
		if gByte != lByte {
			match = "NO <--"
		}

		t.Logf("%4d  %s    %s    %s", i, gStr, lStr, match)
	}

	// Find exact divergence point
	divergeByte := -1
	divergeBit := -1
	for i := 0; i < len(gopusPayload) && i < len(libPayload); i++ {
		if gopusPayload[i] != libPayload[i] {
			divergeByte = i
			xor := gopusPayload[i] ^ libPayload[i]
			for b := 7; b >= 0; b-- {
				if (xor>>b)&1 == 1 {
					divergeBit = 7 - b
					break
				}
			}
			break
		}
	}

	if divergeByte >= 0 {
		t.Log("")
		t.Logf("First divergence at byte %d, bit %d (total bit %d)", divergeByte, divergeBit, divergeByte*8+divergeBit)
	}
}

// TestTransientFlagImpact compares encoding with and without transient flag.
func TestTransientFlagImpact(t *testing.T) {
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

	t.Log("=== Transient Flag Impact Test ===")
	t.Log("")

	// Encode with gopus (default - transient detection enabled)
	gopusEnc := encoder.NewEncoder(sampleRate, 1)
	gopusEnc.SetMode(encoder.ModeCELT)
	gopusEnc.SetBandwidth(gopus.BandwidthFullband)
	gopusEnc.SetBitrate(bitrate)
	gopusPacket, err := gopusEnc.Encode(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}
	t.Logf("Gopus (default): %d bytes, first byte: 0x%02X (%08b)", len(gopusPacket), gopusPacket[1], gopusPacket[1])

	// Check what transient gopus detected
	celtEnc := celt.NewEncoder(1)
	celtEnc.SetBitrate(bitrate)
	preemph := celtEnc.ApplyPreemphasisWithScaling(pcm64)
	overlap := celt.Overlap
	transientInput := make([]float64, overlap+frameSize)
	copy(transientInput[overlap:], preemph)
	result := celtEnc.TransientAnalysis(transientInput, frameSize+overlap, false)
	t.Logf("Gopus transient detection: isTransient=%v, maskMetric=%.2f", result.IsTransient, result.MaskMetric)

	// Encode with libopus (with VBR disabled to match better)
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false) // Disable VBR for fair comparison

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}
	t.Logf("Libopus (VBR=false): %d bytes, first payload byte: 0x%02X (%08b)", len(libPacket), libPacket[1], libPacket[1])

	// Check libopus with VBR enabled
	libEnc2, _ := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	defer libEnc2.Destroy()
	libEnc2.SetBitrate(bitrate)
	libEnc2.SetComplexity(10)
	libEnc2.SetBandwidth(OpusBandwidthFullband)
	libEnc2.SetVBR(true)

	libPacket2, libLen2 := libEnc2.EncodeFloat(pcm32, frameSize)
	if libLen2 > 0 {
		t.Logf("Libopus (VBR=true): %d bytes, first payload byte: 0x%02X (%08b)", len(libPacket2), libPacket2[1], libPacket2[1])
	}

	// Compare payloads
	t.Log("")
	t.Log("=== Payload Comparison ===")
	gopusPayload := gopusPacket[1:]
	libPayload := libPacket[1:]

	minLen := len(gopusPayload)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	// Find divergence
	divergeIdx := -1
	for i := 0; i < minLen; i++ {
		if gopusPayload[i] != libPayload[i] {
			divergeIdx = i
			break
		}
	}

	if divergeIdx == -1 && len(gopusPayload) == len(libPayload) {
		t.Log("SUCCESS: Payloads match!")
	} else {
		if divergeIdx == -1 {
			divergeIdx = minLen
		}
		t.Logf("Divergence at byte %d", divergeIdx)

		// Show first 5 bytes
		t.Log("First 5 bytes comparison:")
		for i := 0; i < 5 && i < minLen; i++ {
			match := "MATCH"
			if gopusPayload[i] != libPayload[i] {
				match = "DIFFER"
			}
			t.Logf("  [%d]: gopus=0x%02X (%08b), libopus=0x%02X (%08b) - %s",
				i, gopusPayload[i], gopusPayload[i], libPayload[i], libPayload[i], match)
		}
	}

	// Analyze bit patterns
	t.Log("")
	t.Log("=== Bit Pattern Analysis ===")
	if len(gopusPayload) > 0 && len(libPayload) > 0 {
		g := gopusPayload[0]
		l := libPayload[0]
		xor := g ^ l
		t.Logf("First payload byte XOR: 0x%02X (%08b)", xor, xor)

		// The range coder writes bytes MSB-first in big-endian order
		// The first bits of the payload are:
		// - Silence flag: logp=15, writes 1 bit if val=0, 15 bits if val=1
		// - Postfilter: logp=1, writes ~1 bit
		// - Transient: logp=3, writes ~1 bit for val=0, ~3 bits for val=1
		// - Intra: logp=3, writes ~3 bits
	}

	// Try to decode with libopus to verify validity
	t.Log("")
	t.Log("=== Decode Verification ===")
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
		if noise > 0 {
			snr := 10.0 * math.Log10(signal/noise)
			t.Logf("Raw SNR: %.2f dB (Q=%.2f)", snr, (snr-48)*100/48)
		}
	} else {
		t.Logf("Libopus failed to decode gopus packet: %d", samples)
	}
}
