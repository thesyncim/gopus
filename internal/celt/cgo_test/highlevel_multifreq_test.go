package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

func TestHighLevelMultiFrequencyPackets(t *testing.T) {
	sampleRate := 48000
	frameSize := 960
	numFrames := 10

	// Multi-frequency signal like compliance test
	totalSamples := frameSize * numFrames
	original := make([]float64, totalSamples)
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
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetBitrate(64000)

	t.Log("=== High-Level Multi-Frequency Packets ===")
	t.Log("Frame  Size (bytes)")

	var totalBytes int
	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		end := start + frameSize
		pcm := original[start:end]

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", f, err)
		}
		totalBytes += len(packet)
		t.Logf("  %2d    %d", f, len(packet))
	}

	t.Logf("\nTotal: %d bytes for %d frames (%.1f bytes/frame avg)",
		totalBytes, numFrames, float64(totalBytes)/float64(numFrames))
	t.Logf("Expected at 64kbps: ~160 bytes/frame (%.1f total)",
		float64(64000*numFrames*frameSize/48000/8))
}
