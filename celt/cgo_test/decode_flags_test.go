//go:build trace
// +build trace

// Package cgo tests decoding flags from encoded bytes.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus"
)

// TestDecodeEncodedFlags decodes flags from actual encoded packets.
func TestDecodeEncodedFlags(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(pcm64[i])
	}

	t.Log("=== Decode Flags from Actual Packets ===")
	t.Log("")

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

	t.Logf("Libopus packet: %d bytes", len(libPacket))
	t.Logf("TOC: 0x%02X", libPacket[0])
	t.Logf("First 5 payload bytes: %02X", libPacket[1:6])

	// Decode flags from libopus payload
	t.Log("")
	t.Log("=== Decoding libopus payload flags ===")
	payload := libPacket[1:]
	decodeFlags(t, payload, "libopus")

	// Now check if the first byte pattern tells us about transient
	t.Log("")
	t.Log("=== First Byte Analysis ===")

	// First byte after different encoding scenarios
	t.Log("Expected first bytes for header-only:")
	t.Log("  trans=0, intra=0: 0x00")
	t.Log("  trans=0, intra=1: 0x64")
	t.Log("  trans=1, intra=0: 0x70")
	t.Log("  trans=1, intra=1: 0x7E")

	// Calculate what the first byte would be with just header encoding
	for _, trans := range []int{0, 1} {
		for _, intra := range []int{0, 1} {
			bits := []int{0, 0, trans, intra}
			logps := []int{15, 1, 3, 3}
			_, headerBytes := TraceBitSequence(bits, logps)
			if len(headerBytes) > 0 {
				t.Logf("  trans=%d, intra=%d: header first byte = 0x%02X", trans, intra, headerBytes[0])
			}
		}
	}

	t.Log("")
	t.Logf("Actual libopus first payload byte: 0x%02X", payload[0])

	// Check if libopus uses trans=1 or trans=0
	// The key observation: if trans=1, there are 3 more bits encoded in the header
	// This affects tell value after header

	// Now encode with gopus and decode its flags
	t.Log("")
	t.Log("=== Gopus Encoding ===")
	gopusEnc := encoder.NewEncoder(sampleRate, 1)
	gopusEnc.SetMode(encoder.ModeCELT)
	gopusEnc.SetBandwidth(gopus.BandwidthFullband)
	gopusEnc.SetBitrate(bitrate)
	gopusPacket, err := gopusEnc.Encode(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}
	t.Logf("Gopus packet: %d bytes", len(gopusPacket))
	t.Logf("TOC: 0x%02X", gopusPacket[0])
	t.Logf("First 5 payload bytes: %02X", gopusPacket[1:6])

	t.Log("")
	t.Log("=== Decoding gopus payload flags ===")
	gopusPayload := gopusPacket[1:]
	decodeFlags(t, gopusPayload, "gopus")

	t.Log("")
	t.Log("=== Summary ===")
	t.Logf("libopus first byte: 0x%02X, gopus first byte: 0x%02X", libPacket[1], gopusPacket[1])
	if libPacket[1] == gopusPacket[1] {
		t.Log("First bytes MATCH!")
	} else {
		t.Logf("First bytes DIFFER by: 0x%02X", libPacket[1]^gopusPacket[1])
	}
}

// decodeFlags uses range decoder to extract flags from payload.
func decodeFlags(t *testing.T, payload []byte, name string) {
	rd := &rangecoding.Decoder{}
	rd.Init(payload)

	t.Logf("Initial: rng=0x%08X, val=0x%08X, tell=%d", rd.Range(), rd.Val(), rd.Tell())

	// Silence flag (logp=15)
	silence := rd.DecodeBit(15)
	t.Logf("After silence: val=%d, rng=0x%08X, val=0x%08X, tell=%d", silence, rd.Range(), rd.Val(), rd.Tell())

	if silence == 1 {
		t.Logf("%s: SILENCE frame detected", name)
		return
	}

	// Postfilter flag (logp=1)
	postfilter := rd.DecodeBit(1)
	t.Logf("After postfilter: val=%d, rng=0x%08X, val=0x%08X, tell=%d", postfilter, rd.Range(), rd.Val(), rd.Tell())

	// Transient flag (logp=3) - only if LM > 0 (20ms frame)
	transient := rd.DecodeBit(3)
	t.Logf("After transient: val=%d, rng=0x%08X, val=0x%08X, tell=%d", transient, rd.Range(), rd.Val(), rd.Tell())

	// Intra flag (logp=3)
	intra := rd.DecodeBit(3)
	t.Logf("After intra: val=%d, rng=0x%08X, val=0x%08X, tell=%d", intra, rd.Range(), rd.Val(), rd.Tell())

	t.Log("")
	t.Logf("%s flags: silence=%d, postfilter=%d, transient=%d, intra=%d", name, silence, postfilter, transient, intra)
}
