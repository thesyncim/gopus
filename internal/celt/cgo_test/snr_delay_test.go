package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestSingleFrameSNRWithDelay(t *testing.T) {
	frameSize := 960
	channels := 1
	freq := 440.0

	// Generate test signal
	original := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		original[i] = 0.5 * math.Sin(2*math.Pi*freq*float64(i)/48000.0)
	}
	original32 := make([]float32, frameSize)
	for i, v := range original {
		original32[i] = float32(v)
	}

	// Encode with gopus
	encoder := celt.NewEncoder(channels)
	encoder.SetBitrate(64000)
	encoded, err := encoder.EncodeFrame(original, frameSize)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	// Decode with libopus
	toc := byte((31 << 3) | 0)
	packet := append([]byte{toc}, encoded...)

	libDec, err := NewLibopusDecoder(48000, channels)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	decoded32, samples := libDec.DecodeFloat(packet, frameSize)
	if samples <= 0 {
		t.Fatalf("libopus decode failed: %d", samples)
	}

	decoded := make([]float64, samples)
	for i := 0; i < samples; i++ {
		decoded[i] = float64(decoded32[i])
	}

	// Search for optimal delay
	bestSNR := -999.0
	bestDelay := 0

	for delay := -500; delay <= 500; delay++ {
		var signal, noise float64
		count := 0

		for i := 0; i < len(original); i++ {
			decIdx := i + delay
			if decIdx >= 0 && decIdx < len(decoded) {
				signal += original[i] * original[i]
				diff := decoded[decIdx] - original[i]
				noise += diff * diff
				count++
			}
		}

		if count > 100 && noise > 0 {
			snr := 10.0 * math.Log10(signal/noise)
			if snr > bestSNR {
				bestSNR = snr
				bestDelay = delay
			}
		}
	}

	t.Logf("Single frame results:")
	t.Logf("  Optimal delay: %d samples (%.2f ms)", bestDelay, float64(bestDelay)*1000/48000)
	t.Logf("  Best SNR: %.2f dB", bestSNR)
	t.Logf("  Q metric: %.2f", (bestSNR-48)*100/48)

	// Show samples at optimal alignment
	t.Log("Samples with optimal alignment:")
	for i := 0; i < 5; i++ {
		decIdx := i + bestDelay
		if decIdx >= 0 && decIdx < len(decoded) {
			t.Logf("  [%d] orig=%.6f decoded[%d]=%.6f", i, original[i], decIdx, decoded[decIdx])
		}
	}

	// Check correlation
	var sumXY, sumX2, sumY2 float64
	for i := 0; i < len(original) && i+bestDelay < len(decoded); i++ {
		if i+bestDelay >= 0 {
			x := original[i]
			y := decoded[i+bestDelay]
			sumXY += x * y
			sumX2 += x * x
			sumY2 += y * y
		}
	}
	correlation := sumXY / math.Sqrt(sumX2*sumY2)
	t.Logf("  Correlation: %.4f", correlation)

	// Also check without delay (raw comparison)
	var rawNoise, rawSignal float64
	for i := 0; i < len(original) && i < len(decoded); i++ {
		rawSignal += original[i] * original[i]
		diff := decoded[i] - original[i]
		rawNoise += diff * diff
	}
	rawSNR := 10.0 * math.Log10(rawSignal/rawNoise)
	t.Logf("  Raw SNR (no delay compensation): %.2f dB", rawSNR)

	// === Now test with libopus encoder for comparison ===
	t.Log("\n=== Libopus encoder comparison ===")

	libEnc, err := NewLibopusEncoder(48000, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(64000)
	libEnc.SetBandwidth(OpusBandwidthFullband)

	libEncoded, n := libEnc.EncodeFloat(original32, frameSize)
	if n < 0 {
		t.Fatalf("libopus encode failed: %d", n)
	}

	// Decode libopus-encoded packet
	libDec2, _ := NewLibopusDecoder(48000, channels)
	defer libDec2.Destroy()

	libDecoded32, libSamples := libDec2.DecodeFloat(libEncoded, frameSize)
	if libSamples <= 0 {
		t.Fatalf("libopus decode of libopus packet failed: %d", libSamples)
	}

	libDecoded := make([]float64, libSamples)
	for i := 0; i < libSamples; i++ {
		libDecoded[i] = float64(libDecoded32[i])
	}

	// Search for optimal delay for libopus roundtrip
	bestLibSNR := -999.0
	bestLibDelay := 0
	for delay := -500; delay <= 500; delay++ {
		var signal, noise float64
		count := 0
		for i := 0; i < len(original); i++ {
			decIdx := i + delay
			if decIdx >= 0 && decIdx < len(libDecoded) {
				signal += original[i] * original[i]
				diff := libDecoded[decIdx] - original[i]
				noise += diff * diff
				count++
			}
		}
		if count > 100 && noise > 0 {
			snr := 10.0 * math.Log10(signal/noise)
			if snr > bestLibSNR {
				bestLibSNR = snr
				bestLibDelay = delay
			}
		}
	}

	t.Logf("Libopus encoder -> libopus decoder:")
	t.Logf("  Optimal delay: %d samples", bestLibDelay)
	t.Logf("  Best SNR: %.2f dB", bestLibSNR)
	t.Logf("  Q metric: %.2f", (bestLibSNR-48)*100/48)

	// Correlation for libopus
	sumXY, sumX2, sumY2 = 0, 0, 0
	for i := 0; i < len(original) && i+bestLibDelay < len(libDecoded); i++ {
		if i+bestLibDelay >= 0 {
			x := original[i]
			y := libDecoded[i+bestLibDelay]
			sumXY += x * y
			sumX2 += x * x
			sumY2 += y * y
		}
	}
	libCorr := sumXY / math.Sqrt(sumX2*sumY2)
	t.Logf("  Correlation: %.4f", libCorr)
}
