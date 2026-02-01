//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus"
)

func TestHighLevelEncoderPacketSizes(t *testing.T) {
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

	t.Log("=== High-Level Encoder Packet Sizes ===")

	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		end := start + frameSize
		pcm := original[start:end]

		packet, err := enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", f, err)
		}

		t.Logf("Frame %d: %d bytes (includes TOC)", f, len(packet))
	}
}
