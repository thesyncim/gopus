//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus"
)

func TestMultiFrequencySignal(t *testing.T) {
	sampleRate := 48000
	frameSize := 960
	numFrames := 5

	// Generate multi-frequency signal like compliance test
	totalSamples := frameSize * numFrames
	original := make([]float64, totalSamples)

	// Multi-frequency: 440 Hz + 1000 Hz + 2000 Hz with amp 0.3 each
	freqs := []float64{440, 1000, 2000}
	amp := 0.3

	for i := 0; i < totalSamples; i++ {
		ti := float64(i) / float64(sampleRate)
		var val float64
		for _, freq := range freqs {
			val += amp * math.Sin(2*math.Pi*freq*ti)
		}
		original[i] = val
	}

	t.Logf("Signal amplitude range: [%.3f, %.3f]", min64(original), max64(original))

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
	decoded := make([]float64, totalSamples)
	for f := 0; f < numFrames; f++ {
		start := f * frameSize

		out, samples := libDec.DecodeFloat(packets[f], frameSize)
		if samples <= 0 {
			t.Fatalf("libopus decode frame %d failed: %d", f, samples)
		}
		for i := 0; i < samples && start+i < totalSamples; i++ {
			decoded[start+i] = float64(out[i])
		}
	}

	// Compute SNR for middle frame
	middleFrame := 2
	frameStart := middleFrame * frameSize
	frameEnd := frameStart + frameSize
	delay := 120

	var signalPower, noisePower float64
	for i := frameStart + delay; i < frameEnd-delay; i++ {
		signalPower += original[i] * original[i]
		noise := original[i] - decoded[i]
		noisePower += noise * noise
	}

	snr := 10 * math.Log10(signalPower/(noisePower+1e-10))
	q := (snr - 48.0) * (100.0 / 48.0)
	t.Logf("Multi-frequency SNR: %.2f dB, Q: %.2f", snr, q)

	// Show samples
	center := frameStart + frameSize/2
	t.Log("\nMiddle frame samples (around center):")
	for i := center - 5; i <= center+5; i++ {
		t.Logf("  [%d] orig=%.5f decoded=%.5f diff=%.5f",
			i, original[i], decoded[i], original[i]-decoded[i])
	}
}

func min64(a []float64) float64 {
	m := a[0]
	for _, v := range a[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func max64(a []float64) float64 {
	m := a[0]
	for _, v := range a[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
