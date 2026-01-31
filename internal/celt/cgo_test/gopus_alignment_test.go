package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

func TestGopusDecodedAlignmentAnalysis(t *testing.T) {
	sampleRate := 48000
	frameSize := 960

	// Generate simple sine wave
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	// Encode with gopus
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)

	gopusPacket, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}
	t.Logf("gopus packet: %d bytes", len(gopusPacket))

	// Decode with libopus
	libDec, _ := NewLibopusDecoder(48000, 1)
	defer libDec.Destroy()

	gopusDecoded, samples := libDec.DecodeFloat(gopusPacket, frameSize)
	if samples <= 0 {
		t.Fatalf("libopus decode of gopus packet failed: %d", samples)
	}

	// Search for best delay alignment
	t.Log("\n=== Finding optimal alignment for gopus ===")
	bestDelay := 0
	bestCorr := -2.0

	for delay := -500; delay <= 500; delay++ {
		corr := computeCrossCorr(pcm, float64Arr(gopusDecoded[:frameSize]), delay)
		if corr > bestCorr {
			bestCorr = corr
			bestDelay = delay
		}
	}
	t.Logf("Best delay: %d samples, correlation: %.4f", bestDelay, bestCorr)

	// Show aligned comparison
	t.Log("\n=== Aligned samples (with best delay) ===")
	for i := 200; i < 210; i++ {
		decIdx := i + bestDelay
		if decIdx >= 0 && decIdx < frameSize {
			t.Logf("  [%d] orig=%.5f, decoded=%.5f, diff=%.5f",
				i, pcm[i], gopusDecoded[decIdx], pcm[i]-float64(gopusDecoded[decIdx]))
		}
	}

	// Compute SNR with alignment
	var signalPower, noisePower float64
	count := 0
	for i := 120; i < frameSize-120; i++ {
		decIdx := i + bestDelay
		if decIdx >= 120 && decIdx < frameSize-120 {
			signalPower += pcm[i] * pcm[i]
			noise := pcm[i] - float64(gopusDecoded[decIdx])
			noisePower += noise * noise
			count++
		}
	}
	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("\nAligned SNR: %.2f dB (over %d samples)", snr, count)

	// Also show unaligned SNR for comparison
	signalPower = 0
	noisePower = 0
	for i := 120; i < frameSize-120; i++ {
		signalPower += pcm[i] * pcm[i]
		noise := pcm[i] - float64(gopusDecoded[i])
		noisePower += noise * noise
	}
	snr0 := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("Unaligned SNR: %.2f dB", snr0)

	// Compare with libopus encoded packet
	t.Log("\n=== Comparing with libopus ===")
	pcm32 := make([]float32, frameSize)
	for i, v := range pcm {
		pcm32[i] = float32(v)
	}

	libEnc, _ := NewLibopusEncoder(48000, 1, OpusApplicationAudio)
	defer libEnc.Destroy()
	libEnc.SetBitrate(64000)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(true)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)

	libDec2, _ := NewLibopusDecoder(48000, 1)
	defer libDec2.Destroy()
	libDecoded, _ := libDec2.DecodeFloat(libPacket, frameSize)

	// Find libopus alignment
	libBestDelay := 0
	libBestCorr := -2.0
	for delay := -500; delay <= 500; delay++ {
		corr := computeCrossCorr(pcm, float64Arr(libDecoded[:frameSize]), delay)
		if corr > libBestCorr {
			libBestCorr = corr
			libBestDelay = delay
		}
	}
	t.Logf("libopus best delay: %d samples, correlation: %.4f", libBestDelay, libBestCorr)

	// Compute libopus aligned SNR
	signalPower = 0
	noisePower = 0
	count = 0
	for i := 120; i < frameSize-120; i++ {
		decIdx := i + libBestDelay
		if decIdx >= 120 && decIdx < frameSize-120 {
			signalPower += pcm[i] * pcm[i]
			noise := pcm[i] - float64(libDecoded[decIdx])
			noisePower += noise * noise
			count++
		}
	}
	libSNR := 10 * math.Log10(signalPower/(noisePower+1e-10))
	t.Logf("libopus aligned SNR: %.2f dB", libSNR)
}

func float64Arr(f32 []float32) []float64 {
	f64 := make([]float64, len(f32))
	for i, v := range f32 {
		f64[i] = float64(v)
	}
	return f64
}

func computeCrossCorr(x, y []float64, delay int) float64 {
	n := len(x)
	if len(y) < n {
		n = len(y)
	}

	var sumXY, sumX2, sumY2 float64
	count := 0
	for i := 0; i < n; i++ {
		yIdx := i + delay
		if yIdx >= 0 && yIdx < len(y) {
			sumXY += x[i] * y[yIdx]
			sumX2 += x[i] * x[i]
			sumY2 += y[yIdx] * y[yIdx]
			count++
		}
	}

	if count == 0 || sumX2 == 0 || sumY2 == 0 {
		return 0
	}
	return sumXY / (math.Sqrt(sumX2) * math.Sqrt(sumY2))
}
