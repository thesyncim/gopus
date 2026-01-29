package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

func TestEncoderDivergenceAnalysis(t *testing.T) {
	sampleRate := 48000
	frameSize := 960

	// Simple single-frequency signal - should be easier to analyze
	pcm := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm[i] = val
		pcm32[i] = float32(val)
	}

	// Encode with gopus high-level encoder
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)

	gopusPacket, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}
	t.Logf("gopus packet: %d bytes", len(gopusPacket))

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
	t.Logf("libopus packet: %d bytes", len(libopusPacket))

	// Decode both packets with libopus
	libDec1, _ := NewLibopusDecoder(48000, 1)
	defer libDec1.Destroy()
	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()

	gopusDecoded, gopusSamples := libDec1.DecodeFloat(gopusPacket, frameSize)
	libDecoded, libSamples := libDec2.DecodeFloat(libopusPacket, frameSize)

	if gopusSamples <= 0 {
		t.Fatalf("gopus decode failed: %d", gopusSamples)
	}
	if libSamples <= 0 {
		t.Fatalf("libopus decode failed: %d", libSamples)
	}

	t.Logf("gopus decoded: %d samples", gopusSamples)
	t.Logf("libopus decoded: %d samples", libSamples)

	// Show first few samples to see if decoder state is the issue
	t.Log("\n=== First 10 decoded samples (after decoder warmup) ===")
	t.Logf("  idx     original     gopus-dec    libopus-dec")
	for i := 0; i < 10; i++ {
		t.Logf("  [%d]  %10.5f  %10.5f  %10.5f",
			i, pcm[i], gopusDecoded[i], libDecoded[i])
	}

	// Show samples in the middle
	t.Log("\n=== Middle samples (480-490) ===")
	t.Logf("  idx     original     gopus-dec    libopus-dec")
	for i := 480; i < 490; i++ {
		t.Logf("  [%d]  %10.5f  %10.5f  %10.5f",
			i, pcm[i], gopusDecoded[i], libDecoded[i])
	}

	// Compute correlation to check if waveform shape is correct
	t.Log("\n=== Correlation Analysis ===")

	// Compute correlation between original and gopus decoded
	gopusCorr := computeCorrelationF64(pcm, float64Slice(gopusDecoded[:frameSize]))
	libCorr := computeCorrelationF64(pcm, float64Slice(libDecoded[:frameSize]))

	t.Logf("Correlation with original: gopus=%.4f, libopus=%.4f", gopusCorr, libCorr)

	// Compute SNR
	gopusSNR := computeSNRWithSkip(pcm, float64Slice(gopusDecoded[:frameSize]), 120)
	libSNR := computeSNRWithSkip(pcm, float64Slice(libDecoded[:frameSize]), 120)

	t.Logf("SNR (skipping first 120 samples): gopus=%.2f dB, libopus=%.2f dB", gopusSNR, libSNR)

	// Show packet byte comparison
	t.Log("\n=== Packet Byte Comparison (first 20 bytes) ===")
	maxLen := 20
	if len(gopusPacket) < maxLen {
		maxLen = len(gopusPacket)
	}
	if len(libopusPacket)-1 < maxLen { // -1 for TOC
		maxLen = len(libopusPacket) - 1
	}

	// gopus packet doesn't include TOC, libopus does
	libPayload := libopusPacket[1:] // Skip TOC

	t.Logf("       gopus      libopus")
	matches := 0
	for i := 0; i < maxLen; i++ {
		g := gopusPacket[i]
		l := libPayload[i]
		match := ""
		if g == l {
			match = " *"
			matches++
		}
		t.Logf("  %2d:  0x%02X       0x%02X%s", i, g, l, match)
	}
	t.Logf("Matching bytes: %d/%d", matches, maxLen)
}

func float64Slice(f32 []float32) []float64 {
	f64 := make([]float64, len(f32))
	for i, v := range f32 {
		f64[i] = float64(v)
	}
	return f64
}

func computeCorrelationF64(x, y []float64) float64 {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}

	var sumX, sumY, sumXY, sumX2, sumY2 float64
	for i := 0; i < n; i++ {
		sumX += x[i]
		sumY += y[i]
		sumXY += x[i] * y[i]
		sumX2 += x[i] * x[i]
		sumY2 += y[i] * y[i]
	}

	num := float64(n)*sumXY - sumX*sumY
	den := math.Sqrt((float64(n)*sumX2 - sumX*sumX) * (float64(n)*sumY2 - sumY*sumY))
	if den == 0 {
		return 0
	}
	return num / den
}

func computeSNRWithSkip(original, decoded []float64, skipSamples int) float64 {
	var signalPower, noisePower float64
	n := len(original)
	if len(decoded) < n {
		n = len(decoded)
	}

	for i := skipSamples; i < n-skipSamples; i++ {
		signalPower += original[i] * original[i]
		noise := original[i] - decoded[i]
		noisePower += noise * noise
	}

	if signalPower == 0 || noisePower == 0 {
		return 0
	}
	return 10 * math.Log10(signalPower/noisePower)
}
