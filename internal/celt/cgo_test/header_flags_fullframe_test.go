//go:build trace
// +build trace

// Package cgo traces header flags in a full frame encode.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceHeaderFlagsFullFrame compares header flag encoding in a full frame.
func TestTraceHeaderFlagsFullFrame(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== Header Flags in Full Frame Encode ===")
	t.Log("")

	// Create gopus CELT encoder directly
	enc := celt.NewEncoder(1)
	enc.SetBitrate(bitrate)

	// We need to trace what the encoder does during EncodeFrame
	// Let's use the encoder to encode and then check the first bytes

	packet, err := enc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	t.Logf("Gopus CELT packet: %d bytes", len(packet))
	showLen := 10
	if showLen > len(packet) {
		showLen = len(packet)
	}
	t.Logf("First %d bytes: %X", showLen, packet[:showLen])
	t.Log("")

	// Now let's decode the first byte to understand what was encoded
	// Range encoder output at position 0 tells us about the header bits
	firstByte := packet[0]
	t.Logf("First byte: 0x%02X = %08b", firstByte, firstByte)

	// Try to decode what this might represent
	rd := &rangecoding.Decoder{}
	rd.Init(packet)

	// Read silence flag (logp=15)
	silence := rd.DecodeBit(15)
	t.Logf("Decoded silence: %d", silence)

	// Read postfilter (logp=1)
	postfilter := rd.DecodeBit(1)
	t.Logf("Decoded postfilter: %d", postfilter)

	// Read transient (logp=3)
	transient := rd.DecodeBit(3)
	t.Logf("Decoded transient: %d", transient)

	// Read intra (logp=3)
	intra := rd.DecodeBit(3)
	t.Logf("Decoded intra: %d", intra)

	t.Log("")
	t.Logf("Summary: silence=%d, postfilter=%d, transient=%d, intra=%d",
		silence, postfilter, transient, intra)

	// Now let's manually encode these flags and compare bytes
	t.Log("")
	t.Log("=== Manual Header Encoding ===")

	// Expected: silence=0, postfilter=0, transient=0, intra=1
	headerBits := []int{0, 0, 0, 1}
	headerLogps := []int{15, 1, 3, 3}

	// Encode manually with gopus range encoder
	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	for i := 0; i < 4; i++ {
		re.EncodeBit(headerBits[i], uint(headerLogps[i]))
		t.Logf("After %s=%d: rng=0x%08X, val=0x%08X, tell=%d",
			[]string{"silence", "postfilter", "transient", "intra"}[i],
			headerBits[i], re.Range(), re.Val(), re.Tell())
	}

	manualBytes := re.Done()
	t.Logf("Manual header bytes: %X", manualBytes)

	// Compare with libopus
	libStates, libBytes := TraceBitSequence(headerBits, headerLogps)
	if libStates == nil {
		t.Fatal("libopus trace failed")
	}
	t.Logf("Libopus header bytes: %X", libBytes)

	// Compare
	if len(manualBytes) > 0 && len(libBytes) > 0 {
		if manualBytes[0] == libBytes[0] {
			t.Log("Manual encoding MATCHES libopus header")
		} else {
			t.Logf("Manual encoding DIFFERS: gopus=0x%02X, libopus=0x%02X", manualBytes[0], libBytes[0])
		}
	}

	// Compare with actual encoder output
	t.Log("")
	t.Log("=== Comparison with Full Encoder Output ===")
	t.Logf("Full encoder first byte: 0x%02X", packet[0])
	if len(manualBytes) > 0 {
		t.Logf("Manual header first byte: 0x%02X", manualBytes[0])
	}
	if len(libBytes) > 0 {
		t.Logf("Libopus header first byte: 0x%02X", libBytes[0])
	}
}
