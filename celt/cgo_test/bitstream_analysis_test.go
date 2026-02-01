//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides tests to analyze the bitstream structure.
// Agent 22: Debug bitstream divergence at byte 7
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestBitstreamAnalysis decodes both gopus and libopus packets to trace bit positions
func TestBitstreamAnalysis(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a 440Hz sine wave
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*440.0*float64(i)/48000.0)
	}

	// Encode with libopus
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

	// Decode the libopus packet to trace bit positions
	libPayload := libBytes[1:] // Skip TOC byte

	t.Logf("libopus payload: %d bytes", len(libPayload))
	t.Logf("First 16 bytes: %02x", libPayload[:minInt(16, len(libPayload))])

	// Decode flags and trace bit positions
	rd := &rangecoding.Decoder{}
	rd.Init(libPayload)

	t.Log("\n=== Decoding libopus packet ===")

	// 1. Silence flag (if tell==1)
	tellBefore := rd.Tell()
	if rd.Tell() == 1 {
		silence := rd.DecodeBit(15)
		t.Logf("Silence flag (tell=%d): %d", tellBefore, silence)
	}

	// 2. Postfilter flag
	tellBefore = rd.Tell()
	postfilter := rd.DecodeBit(1)
	t.Logf("Postfilter flag (tell=%d->%d): %d", tellBefore, rd.Tell(), postfilter)

	// 3. Transient flag (LM=3 > 0)
	tellBefore = rd.Tell()
	transient := rd.DecodeBit(3)
	t.Logf("Transient flag (tell=%d->%d): %d", tellBefore, rd.Tell(), transient)

	// 4. Intra flag
	tellBefore = rd.Tell()
	intra := rd.DecodeBit(3)
	t.Logf("Intra flag (tell=%d->%d): %d", tellBefore, rd.Tell(), intra)

	t.Logf("\nAfter flags: tell=%d (bit %d, byte ~%.1f)", rd.Tell(), rd.Tell(), float64(rd.Tell())/8)

	// Now decode coarse energy to find where it ends
	// This is complex because it uses Laplace coding
	// For now, let's just note the position

	t.Log("\n=== Analyzing gopus encoding ===")

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)
	encoder.SetComplexity(10)

	gopusBytes, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	t.Logf("gopus payload: %d bytes", len(gopusBytes))
	t.Logf("First 16 bytes: %02x", gopusBytes[:minInt(16, len(gopusBytes))])

	// Compare bytes
	t.Log("\n=== Byte comparison ===")
	for i := 0; i < minInt(16, minInt(len(gopusBytes), len(libPayload))); i++ {
		match := ""
		if gopusBytes[i] != libPayload[i] {
			match = " <-- DIFF"
		}
		t.Logf("[%2d] gopus=%02X libopus=%02X%s", i, gopusBytes[i], libPayload[i], match)
	}

	// Calculate bit position of divergence
	divergeIdx := -1
	for i := 0; i < minInt(len(gopusBytes), len(libPayload)); i++ {
		if gopusBytes[i] != libPayload[i] {
			divergeIdx = i
			break
		}
	}

	if divergeIdx >= 0 {
		gByte := gopusBytes[divergeIdx]
		lByte := libPayload[divergeIdx]
		xorDiff := gByte ^ lByte

		// Find first differing bit within the byte
		firstDiffBit := 0
		for b := 7; b >= 0; b-- {
			if (xorDiff>>b)&1 == 1 {
				firstDiffBit = 7 - b
				break
			}
		}

		t.Logf("\nDivergence details:")
		t.Logf("  Byte %d, first different bit at position %d within byte", divergeIdx, firstDiffBit)
		t.Logf("  Absolute bit position: ~%d", divergeIdx*8+firstDiffBit)
		t.Logf("  gopus byte:   %02X = %08b", gByte, gByte)
		t.Logf("  libopus byte: %02X = %08b", lByte, lByte)
		t.Logf("  XOR diff:     %02X = %08b", xorDiff, xorDiff)
	}
}

// TestTraceGopusEncoding traces gopus encoding step by step
func TestTraceGopusEncoding(t *testing.T) {
	frameSize := 960
	channels := 1

	// Generate a 440Hz sine wave
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = 0.5 * math.Sin(2.0*math.Pi*440.0*float64(i)/48000.0)
	}

	// Create encoder with tracing
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(64000)
	encoder.SetComplexity(10)

	// We'll need to add tracing to the encoder to see bit positions
	// For now, let's just encode and look at the output
	gopusBytes, err := encoder.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	t.Logf("Encoded %d bytes", len(gopusBytes))

	// Try to decode what we just encoded to verify it's valid
	decoder := celt.NewDecoder(channels)
	decoded, err := decoder.DecodeFrame(gopusBytes, frameSize)
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	// Check decoded signal
	var sum float64
	for _, s := range decoded {
		sum += math.Abs(s)
	}
	mean := sum / float64(len(decoded))
	t.Logf("Decoded signal mean: %.6f (expected ~0.32 for 0.5 amplitude sine)", mean)
}
