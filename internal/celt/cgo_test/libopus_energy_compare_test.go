// Package cgo compares band energies between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestLibopusEnergyCompare checks if gopus and libopus compute similar energies.
func TestLibopusEnergyCompare(t *testing.T) {
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

	t.Log("=== Gopus vs Libopus Band Energy Comparison ===")
	t.Log("")

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	shortBlocks := mode.ShortBlocks // = 8 for transient

	// Gopus: compute band energies
	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(bitrate)

	preemph := enc.ApplyPreemphasisWithScaling(pcm64)
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, enc.OverlapBuffer(), shortBlocks)
	gopusEnergies := enc.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)

	t.Log("Gopus band energies (transient mode, shortBlocks=8):")
	for i := 0; i < 10 && i < len(gopusEnergies); i++ {
		t.Logf("  Band %d: %.4f", i, gopusEnergies[i])
	}

	// Libopus: encode with libopus and decode band info
	t.Log("")
	t.Log("Libopus packet analysis:")

	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	t.Logf("Libopus packet: %d bytes", len(libPacket))
	t.Logf("First 10 payload bytes: %02X", libPacket[1:11])

	// We can't directly get libopus energies, but we can:
	// 1. Decode the packet and compare decoded audio
	// 2. Try to reverse-engineer the encoded energies from the packet

	// Decode with libopus
	libDec, _ := NewLibopusDecoder(sampleRate, 1)
	defer libDec.Destroy()

	decoded32, samples := libDec.DecodeFloat(libPacket, frameSize)
	if samples <= 0 {
		t.Fatalf("libopus decode failed: %d", samples)
	}

	// Compute SNR for libopus roundtrip
	var signal, noise float64
	for i := 0; i < frameSize && i < samples; i++ {
		ref := pcm64[i]
		dec := float64(decoded32[i])
		signal += ref * ref
		diff := dec - ref
		noise += diff * diff
	}
	snr := 10.0 * math.Log10(signal/noise)
	t.Logf("Libopus roundtrip SNR: %.2f dB (Q=%.2f)", snr, (snr-48)*100/48)

	// Now try gopus roundtrip
	t.Log("")
	t.Log("Gopus packet analysis:")

	gopusPacket, err := enc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Add TOC for libopus decoder
	toc := byte((31 << 3) | 0) // CELT fullband, mono, 20ms
	fullPacket := append([]byte{toc}, gopusPacket...)

	t.Logf("Gopus packet: %d bytes", len(gopusPacket))
	t.Logf("First 10 bytes: %02X", gopusPacket[:minIntLibEnergy(10, len(gopusPacket))])

	decoded32Gopus, samplesGopus := libDec.DecodeFloat(fullPacket, frameSize)
	if samplesGopus <= 0 {
		t.Logf("libopus failed to decode gopus packet: %d", samplesGopus)
	} else {
		var signal2, noise2 float64
		for i := 0; i < frameSize && i < samplesGopus; i++ {
			ref := pcm64[i]
			dec := float64(decoded32Gopus[i])
			signal2 += ref * ref
			diff := dec - ref
			noise2 += diff * diff
		}
		snr2 := 10.0 * math.Log10(signal2/noise2)
		t.Logf("Gopus -> libopus decode SNR: %.2f dB (Q=%.2f)", snr2, (snr2-48)*100/48)
	}

	// Compare packets byte by byte
	t.Log("")
	t.Log("=== Packet Comparison ===")
	libPayload := libPacket[1:] // Skip TOC
	gopusPayload := gopusPacket

	minLen := len(libPayload)
	if len(gopusPayload) < minLen {
		minLen = len(gopusPayload)
	}

	t.Logf("Libopus payload: %d bytes", len(libPayload))
	t.Logf("Gopus payload: %d bytes", len(gopusPayload))

	matchingBytes := 0
	for i := 0; i < minLen; i++ {
		if libPayload[i] == gopusPayload[i] {
			matchingBytes++
		}
	}
	t.Logf("Matching bytes: %d / %d (%.1f%%)", matchingBytes, minLen, 100*float64(matchingBytes)/float64(minLen))

	// First 5 bytes detailed comparison
	t.Log("First 5 bytes:")
	for i := 0; i < 5 && i < minLen; i++ {
		match := "MATCH"
		if libPayload[i] != gopusPayload[i] {
			match = "DIFFER"
		}
		t.Logf("  [%d]: gopus=0x%02X, libopus=0x%02X - %s", i, gopusPayload[i], libPayload[i], match)
	}
}

func minIntLibEnergy(a, b int) int {
	if a < b {
		return a
	}
	return b
}
