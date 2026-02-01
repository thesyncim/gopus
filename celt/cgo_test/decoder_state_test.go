//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

func TestDecoderStateEffect(t *testing.T) {
	frameSize := 960

	// Generate sine wave
	pcmSine := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcmSine {
		ti := float64(i) / 48000.0
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcmSine[i] = val
		pcm32[i] = float32(val)
	}

	t.Log("=== Testing if Decoder State Affects Output ===")

	// Encode with libopus to get a valid packet
	libEnc, _ := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	defer libEnc.Destroy()
	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)
	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	t.Logf("Libopus packet: %d bytes", len(libPacket))

	// Test 1: Fresh decoder
	t.Log("\n--- Test 1: Fresh decoder ---")
	dec1, _ := NewLibopusDecoder(48000, 1)
	defer dec1.Destroy()
	decoded1, _ := dec1.DecodeFloat(libPacket, frameSize)
	t.Logf("Original [400]: %.4f", pcmSine[400])
	t.Logf("Decoded  [400]: %.4f", decoded1[400])

	// Test 2: Decoder after decoding another packet
	t.Log("\n--- Test 2: Decoder after decoding DC packet ---")

	// Create DC packet
	pcmDC := make([]float32, frameSize)
	for i := range pcmDC {
		pcmDC[i] = 0.3
	}
	libEnc2, _ := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	defer libEnc2.Destroy()
	libEnc2.SetBitrate(64000)
	libEnc2.SetComplexity(10)
	libEnc2.SetBandwidth(OpusBandwidthFullband)
	libEnc2.SetVBR(true)
	dcPacket, _ := libEnc2.EncodeFloat(pcmDC, frameSize)

	dec2, _ := NewLibopusDecoder(48000, 1)
	defer dec2.Destroy()

	// First decode DC
	dec2.DecodeFloat(dcPacket, frameSize)
	// Then decode sine
	decoded2, _ := dec2.DecodeFloat(libPacket, frameSize)
	t.Logf("Original [400]: %.4f", pcmSine[400])
	t.Logf("Decoded  [400]: %.4f", decoded2[400])

	// Test 3: Fresh gopus packet decoding
	t.Log("\n--- Test 3: Fresh decoder for gopus packet ---")

	// Encode with gopus
	celtEnc := celt.NewEncoder(1)
	celtEnc.SetBitrate(64000)
	gopusCeltPacket, _ := celtEnc.EncodeFrame(pcmSine, frameSize)

	toc := byte(0xF8)
	gopusWithTOC := append([]byte{toc}, gopusCeltPacket...)

	dec3, _ := NewLibopusDecoder(48000, 1)
	defer dec3.Destroy()
	decoded3, _ := dec3.DecodeFloat(gopusWithTOC, frameSize)
	t.Logf("Original [400]: %.4f", pcmSine[400])
	t.Logf("Decoded  [400]: %.4f", decoded3[400])

	// Compare results
	t.Log("\n--- Summary ---")
	t.Logf("Fresh libopus decode:  %.4f", decoded1[400])
	t.Logf("After DC libopus:      %.4f", decoded2[400])
	t.Logf("Fresh gopus decode:    %.4f", decoded3[400])
}
