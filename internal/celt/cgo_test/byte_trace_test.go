// Package cgo traces byte-by-byte divergence between gopus and libopus packets.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestByteByByteTrace compares packets byte by byte.
func TestByteByByteTrace(t *testing.T) {
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

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}
	libPayload := libPacket[1:] // Skip TOC byte

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	t.Log("=== Byte-by-byte comparison ===")
	t.Logf("gopus packet length: %d", len(goPacket))
	t.Logf("libopus payload length: %d", len(libPayload))

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

	t.Logf("\nFirst divergence at byte %d", firstDiff)
	t.Log("Bytes around divergence:")
	start := firstDiff - 3
	if start < 0 {
		start = 0
	}
	end := firstDiff + 5
	if end > minLen {
		end = minLen
	}

	t.Log("Byte | gopus | libopus | Match")
	t.Log("-----+-------+---------+------")
	for i := start; i < end; i++ {
		match := "  "
		if goPacket[i] == libPayload[i] {
			match = "OK"
		} else {
			match = "!!"
		}
		t.Logf("%4d |  0x%02x |   0x%02x  | %s", i, goPacket[i], libPayload[i], match)
	}

	// Show bits around divergence
	t.Log("\nBit-level view around divergence:")
	bitStart := firstDiff * 8
	t.Logf("Divergence at byte %d = bit %d to %d", firstDiff, bitStart, bitStart+7)

	// What's encoded around this point?
	t.Log("\nBased on typical mono 960-sample encoding at 64kbps:")
	t.Log("  - Header flags: bits 0-6")
	t.Log("  - Coarse energy: bits 7-84")
	t.Log("  - TF encoding: bits 85-94")
	t.Log("  - Spread: bits 95-96")
	t.Log("  - Dynalloc: bits 97-106")
	t.Log("  - Trim: bits 107-109")
	t.Log("  - Allocation: bits 110+")
	t.Log("  - Fine energy: varies")
	t.Log("  - PVQ bands: remaining")

	divergeBit := firstDiff * 8
	if divergeBit < 7 {
		t.Log("Divergence in HEADER FLAGS region")
	} else if divergeBit < 85 {
		t.Log("Divergence in COARSE ENERGY region")
	} else if divergeBit < 95 {
		t.Log("Divergence in TF ENCODING region")
	} else if divergeBit < 97 {
		t.Log("Divergence in SPREAD region")
	} else if divergeBit < 107 {
		t.Log("Divergence in DYNALLOC region")
	} else if divergeBit < 110 {
		t.Log("Divergence in TRIM region")
	} else {
		t.Log("Divergence in ALLOCATION / FINE ENERGY / PVQ BANDS region")
	}

	// Show final range comparison
	t.Log("")
	t.Logf("Final range: gopus=0x%08X, libopus=0x%08X", goEnc.FinalRange(), libEnc.GetFinalRange())
}
