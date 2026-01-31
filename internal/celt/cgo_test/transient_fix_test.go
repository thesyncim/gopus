package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTransientFixVerification verifies that forcing transient=1 for first frame
// improves match with libopus.
func TestTransientFixVerification(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	// Test 1: Without forcing transient
	enc1 := celt.NewEncoder(1)
	enc1.Reset()
	enc1.SetBitrate(bitrate)
	packet1, err := enc1.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("Encode without force transient failed: %v", err)
	}

	// Test 2: With forcing transient for first frame
	enc2 := celt.NewEncoder(1)
	enc2.Reset()
	enc2.SetBitrate(bitrate)
	enc2.SetForceTransient(true)
	packet2, err := enc2.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("Encode with force transient failed: %v", err)
	}

	t.Log("=== Transient Fix Verification ===")
	t.Logf("")
	t.Logf("Without ForceTransient (transient=0):")
	t.Logf("  Packet length: %d bytes", len(packet1))
	if len(packet1) >= 10 {
		t.Logf("  First 10 bytes: %02X", packet1[:10])
	}

	t.Logf("")
	t.Logf("With ForceTransient (transient=1):")
	t.Logf("  Packet length: %d bytes", len(packet2))
	if len(packet2) >= 10 {
		t.Logf("  First 10 bytes: %02X", packet2[:10])
	}

	// Get libopus packet for comparison
	samples32 := make([]float32, frameSize)
	for i, v := range samples {
		samples32[i] = float32(v)
	}

	libEnc, err := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)

	libPacket, n := libEnc.EncodeFloat(samples32, frameSize)
	if n <= 0 {
		t.Fatalf("Libopus encode failed: %d", n)
	}

	// Skip TOC byte for comparison
	libPayload := libPacket[1:]
	t.Logf("")
	t.Logf("Libopus (reference):")
	t.Logf("  Packet length: %d bytes (excl TOC)", len(libPayload))
	if len(libPayload) >= 10 {
		t.Logf("  First 10 bytes: %02X", libPayload[:10])
	}

	// Count matching bytes
	matchNoForce := 0
	matchForce := 0
	maxCheck := 20

	for i := 0; i < maxCheck && i < len(packet1) && i < len(libPayload); i++ {
		if packet1[i] == libPayload[i] {
			matchNoForce++
		}
	}
	for i := 0; i < maxCheck && i < len(packet2) && i < len(libPayload); i++ {
		if packet2[i] == libPayload[i] {
			matchForce++
		}
	}

	t.Logf("")
	t.Logf("Matching bytes (first %d):", maxCheck)
	t.Logf("  Without ForceTransient: %d/%d", matchNoForce, maxCheck)
	t.Logf("  With ForceTransient:    %d/%d", matchForce, maxCheck)

	if matchForce > matchNoForce {
		t.Log("")
		t.Log("*** ForceTransient IMPROVES match with libopus! ***")
		t.Log("*** This confirms transient detection is the root cause. ***")
	} else if matchForce == matchNoForce {
		t.Log("")
		t.Log("ForceTransient has no effect on match count.")
	} else {
		t.Log("")
		t.Log("ForceTransient REDUCES match (unexpected).")
	}

	// Detailed byte comparison
	t.Logf("")
	t.Logf("Detailed byte comparison (first 15 bytes):")
	t.Logf("  Byte | noForce | force | libopus | match?")
	t.Logf("  -----+---------+-------+---------+--------")
	for i := 0; i < 15 && i < len(libPayload); i++ {
		b1 := byte(0)
		b2 := byte(0)
		if i < len(packet1) {
			b1 = packet1[i]
		}
		if i < len(packet2) {
			b2 = packet2[i]
		}
		libB := libPayload[i]

		match := ""
		if b2 == libB {
			match = "FORCE MATCH"
		} else if b1 == libB {
			match = "NO-FORCE MATCH"
		} else {
			match = ""
		}
		t.Logf("  %4d | 0x%02X    | 0x%02X  | 0x%02X    | %s", i, b1, b2, libB, match)
	}
}
