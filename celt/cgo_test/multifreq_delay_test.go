//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus"
)

func TestMultiFrequencyDelaySearch(t *testing.T) {
	sampleRate := 48000
	frameSize := 960
	numFrames := 10

	// Generate multi-frequency signal like compliance test
	totalSamples := frameSize * numFrames
	original := make([]float64, totalSamples)

	// Multi-frequency: 440 Hz + 1000 Hz + 2000 Hz with amp 0.3 each
	freqs := []float64{440, 1000, 2000}
	amp := 0.3

	for i := 0; i < totalSamples; i++ {
		ti := float64(i) / float64(sampleRate)
		for _, freq := range freqs {
			original[i] += amp * math.Sin(2*math.Pi*freq*ti)
		}
	}

	// Create high-level encoder
	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(gopus.BandwidthFullband)
	enc.SetBitrate(64000)

	// Encode all frames
	packets := make([][]byte, numFrames)
	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		end := start + frameSize
		pcm := original[start:end]

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", f, err)
		}
		packets[f] = packet
		t.Logf("Frame %d: %d bytes", f, len(packet))
	}

	// Create libopus decoder
	libDec, err := NewLibopusDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	// Decode all frames
	decoded := make([]float64, totalSamples+frameSize) // Extra room for delay
	for f := 0; f < numFrames; f++ {
		start := f * frameSize

		out, samples := libDec.DecodeFloat(packets[f], frameSize)
		if samples <= 0 {
			t.Fatalf("libopus decode frame %d failed: %d", f, samples)
		}
		for i := 0; i < samples && start+i < len(decoded); i++ {
			decoded[start+i] = float64(out[i])
		}
	}

	// Search for best delay
	t.Log("\n=== Delay Search ===")
	bestDelay := 0
	bestSNR := math.Inf(-1)

	for delay := -1000; delay <= 1000; delay++ {
		var signalPower, noisePower float64
		count := 0

		// Compare in the middle of the signal (after warmup)
		startIdx := frameSize * 2 // Start after 2 frames warmup
		endIdx := frameSize * 8   // End before last 2 frames

		for i := startIdx; i < endIdx; i++ {
			decodedIdx := i + delay
			if decodedIdx >= 0 && decodedIdx < len(decoded) {
				signalPower += original[i] * original[i]
				noise := original[i] - decoded[decodedIdx]
				noisePower += noise * noise
				count++
			}
		}

		if count > 0 && signalPower > 0 {
			snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
			if snr > bestSNR {
				bestSNR = snr
				bestDelay = delay
			}
		}
	}

	q := (bestSNR - 48.0) * (100.0 / 48.0)
	t.Logf("Best delay: %d samples", bestDelay)
	t.Logf("Best SNR: %.2f dB, Q: %.2f", bestSNR, q)

	// Show samples with best delay
	t.Log("\nSample comparison (around sample 3000, with best delay):")
	for i := 3000; i < 3010; i++ {
		decodedIdx := i + bestDelay
		if decodedIdx >= 0 && decodedIdx < len(decoded) {
			t.Logf("  [%d] orig=%.5f decoded=%.5f diff=%.5f",
				i, original[i], decoded[decodedIdx], original[i]-decoded[decodedIdx])
		}
	}

	// Also test with delay = 0 for comparison
	t.Log("\n=== Without delay compensation (delay=0) ===")
	var signalPower0, noisePower0 float64
	startIdx := frameSize * 2
	endIdx := frameSize * 8
	for i := startIdx; i < endIdx; i++ {
		signalPower0 += original[i] * original[i]
		noise := original[i] - decoded[i]
		noisePower0 += noise * noise
	}
	snr0 := 10 * math.Log10(signalPower0/(noisePower0+1e-10))
	q0 := (snr0 - 48.0) * (100.0 / 48.0)
	t.Logf("SNR with delay=0: %.2f dB, Q: %.2f", snr0, q0)
}
