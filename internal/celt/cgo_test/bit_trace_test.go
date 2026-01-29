package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

func TestBitByBitComparison(t *testing.T) {
	frameSize := 960

	// Generate simple DC signal
	pcm := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm {
		pcm[i] = 0.3 // Constant DC
		pcm32[i] = 0.3
	}

	t.Log("=== Bit-by-Bit Comparison: DC Signal ===")

	// Gopus encode
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)

	gopusPacket, _ := enc.Encode(pcm, frameSize)
	t.Logf("Gopus packet: %d bytes", len(gopusPacket))
	t.Logf("Gopus first 20 bytes: %02X", gopusPacket[:minI(20, len(gopusPacket))])

	// Libopus encode
	libEnc, _ := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	defer libEnc.Destroy()
	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	t.Logf("Libopus packet: %d bytes", len(libPacket))
	t.Logf("Libopus TOC: 0x%02X", libPacket[0])
	t.Logf("Libopus first 20 bytes: %02X", libPacket[1:minI(21, len(libPacket))])

	// Binary compare first 10 bytes
	t.Log("\n=== Binary Comparison ===")
	t.Log("Byte  gopus     libopus   XOR")
	libPayload := libPacket[1:] // Skip TOC
	for i := 0; i < 10 && i < len(gopusPacket) && i < len(libPayload); i++ {
		g := gopusPacket[i]
		l := libPayload[i]
		xor := g ^ l
		match := ""
		if g == l {
			match = " (match)"
		}
		t.Logf("  %2d:  %08b  %08b  %08b%s", i, g, l, xor, match)
	}

	// Now test with SINE wave
	t.Log("\n\n=== Bit-by-Bit Comparison: 440Hz Sine ===")
	pcmSine := make([]float64, frameSize)
	pcm32Sine := make([]float32, frameSize)
	for i := range pcmSine {
		ti := float64(i) / 48000.0
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcmSine[i] = val
		pcm32Sine[i] = float32(val)
	}

	enc2 := encoder.NewEncoder(48000, 1)
	enc2.SetMode(encoder.ModeCELT)
	enc2.SetBandwidth(types.BandwidthFullband)
	enc2.SetBitrate(64000)

	gopusPacket2, _ := enc2.Encode(pcmSine, frameSize)
	t.Logf("Gopus packet: %d bytes", len(gopusPacket2))
	t.Logf("Gopus first 20 bytes: %02X", gopusPacket2[:minI(20, len(gopusPacket2))])

	libEnc2, _ := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	defer libEnc2.Destroy()
	libEnc2.SetBitrate(64000)
	libEnc2.SetComplexity(10)
	libEnc2.SetBandwidth(OpusBandwidthFullband)
	libEnc2.SetVBR(true)

	libPacket2, _ := libEnc2.EncodeFloat(pcm32Sine, frameSize)
	t.Logf("Libopus packet: %d bytes", len(libPacket2))
	t.Logf("Libopus first 20 bytes: %02X", libPacket2[1:minI(21, len(libPacket2))])

	// Binary compare first 10 bytes
	t.Log("\n=== Binary Comparison (Sine) ===")
	t.Log("Byte  gopus     libopus   XOR")
	libPayload2 := libPacket2[1:]
	for i := 0; i < 10 && i < len(gopusPacket2) && i < len(libPayload2); i++ {
		g := gopusPacket2[i]
		l := libPayload2[i]
		xor := g ^ l
		match := ""
		if g == l {
			match = " (match)"
		}
		t.Logf("  %2d:  %08b  %08b  %08b%s", i, g, l, xor, match)
	}

	// Decode both and compare
	t.Log("\n=== Decode Comparison (Sine) ===")
	dec1, _ := NewLibopusDecoder(48000, 1)
	defer dec1.Destroy()
	dec2, _ := NewLibopusDecoder(48000, 1)
	defer dec2.Destroy()

	toc := byte(0xF8)
	gopusWithTOC := append([]byte{toc}, gopusPacket2...)
	gopusDec, _ := dec1.DecodeFloat(gopusWithTOC, frameSize)
	libDec, _ := dec2.DecodeFloat(libPacket2, frameSize)

	// Compare middle samples
	t.Log("Middle samples (400-405):")
	for i := 400; i < 405; i++ {
		t.Logf("  [%d] orig=%.4f, gopusDec=%.4f, libDec=%.4f",
			i, pcmSine[i], gopusDec[i], libDec[i])
	}

	// Compute SNR
	var signalPower, gopusNoisePower, libNoisePower float64
	for i := 100; i < frameSize-100; i++ {
		signalPower += pcmSine[i] * pcmSine[i]
		gopusNoise := float64(gopusDec[i]) - pcmSine[i]
		libNoise := float64(libDec[i]) - pcmSine[i]
		gopusNoisePower += gopusNoise * gopusNoise
		libNoisePower += libNoise * libNoise
	}
	gopusSNR := 10 * math.Log10(signalPower/(gopusNoisePower+1e-10))
	libSNR := 10 * math.Log10(signalPower/(libNoisePower+1e-10))
	t.Logf("\nSNR: gopus=%.1f dB, libopus=%.1f dB", gopusSNR, libSNR)
}

func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}
