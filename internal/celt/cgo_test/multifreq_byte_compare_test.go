package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestMultiFreqByteCompare(t *testing.T) {
	sampleRate := 48000
	frameSize := 960

	// Multi-frequency signal
	pcm := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	freqs := []float64{440, 1000, 2000}
	amp := 0.3
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		for _, freq := range freqs {
			pcm[i] += amp * math.Sin(2*math.Pi*freq*ti)
			pcm32[i] += float32(amp * math.Sin(2*math.Pi*freq*ti))
		}
	}

	// Encode with gopus
	gopusEnc := celt.NewEncoder(1)
	gopusEnc.SetBitrate(64000)
	gopusPacket, err := gopusEnc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()

	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)

	libopusPacket, n := libEnc.EncodeFloat(pcm32, frameSize)
	if n < 0 {
		t.Fatalf("libopus encode failed: %d", n)
	}
	libPayload := libopusPacket[1:] // Skip TOC

	t.Logf("gopus:   %d bytes", len(gopusPacket))
	t.Logf("libopus: %d bytes (excl TOC)", len(libPayload))

	t.Log("\nByte comparison (first 40 bytes):")
	t.Logf("       gopus      libopus   match?")
	maxLen := 40
	if len(gopusPacket) < maxLen {
		maxLen = len(gopusPacket)
	}
	if len(libPayload) < maxLen {
		maxLen = len(libPayload)
	}

	for i := 0; i < maxLen; i++ {
		g := gopusPacket[i]
		l := libPayload[i]
		match := ""
		if g != l {
			match = " <-- diff"
		}
		t.Logf("  %2d:  0x%02X       0x%02X    %s", i, g, l, match)
	}

	// Decode both with libopus and compare
	libDec, err := NewLibopusDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	// Decode gopus packet
	toc := byte((31 << 3) | 0)
	gopusWithTOC := append([]byte{toc}, gopusPacket...)
	gopusDecoded, gopusSamples := libDec.DecodeFloat(gopusWithTOC, frameSize)
	if gopusSamples <= 0 {
		t.Fatalf("gopus decode failed: %d", gopusSamples)
	}

	// New decoder for libopus packet
	libDec2, err := NewLibopusDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewLibopusDecoder2 failed: %v", err)
	}
	defer libDec2.Destroy()

	libDecoded, libSamples := libDec2.DecodeFloat(libopusPacket, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	// Compare decoded outputs
	t.Log("\nDecoded samples comparison (middle of frame):")
	center := frameSize / 2
	t.Logf("  idx     original  gopus-dec  libopus-dec")
	for i := center - 5; i <= center+5; i++ {
		t.Logf("  [%d] %10.5f %10.5f %10.5f", i, pcm[i], gopusDecoded[i], libDecoded[i])
	}

	// Compute SNR for each
	var gopusSignal, gopusNoise, libSignal, libNoise float64
	for i := 120; i < frameSize-120; i++ {
		gopusSignal += pcm[i] * pcm[i]
		gopusNoise += math.Pow(pcm[i]-float64(gopusDecoded[i]), 2)
		libSignal += pcm[i] * pcm[i]
		libNoise += math.Pow(pcm[i]-float64(libDecoded[i]), 2)
	}
	gopusSNR := 10 * math.Log10(gopusSignal/(gopusNoise+1e-10))
	libSNR := 10 * math.Log10(libSignal/(libNoise+1e-10))

	t.Logf("\ngopus-encoded SNR:   %.2f dB", gopusSNR)
	t.Logf("libopus-encoded SNR: %.2f dB", libSNR)
}
