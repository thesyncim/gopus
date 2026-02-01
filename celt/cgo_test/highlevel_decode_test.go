//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus"
)

func TestHighLevelEncoderWithLibopusDecode(t *testing.T) {
	sampleRate := 48000
	frameSize := 960
	freq := 440.0
	numFrames := 5

	// Generate signal
	totalSamples := frameSize * numFrames
	original := make([]float64, totalSamples)
	for i := range original {
		ti := float64(i) / float64(sampleRate)
		original[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
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

		// Packets from high-level encoder include TOC
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
	t.Logf("High-level encoder -> libopus decoder SNR: %.2f dB", snr)

	// Show samples
	center := frameStart + frameSize/2
	t.Log("\nMiddle frame samples (around center):")
	for i := center - 5; i <= center+5; i++ {
		t.Logf("  [%d] orig=%.5f decoded=%.5f diff=%.5f",
			i, original[i], decoded[i], original[i]-decoded[i])
	}
}
