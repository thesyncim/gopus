package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestVBRTargetTrace(t *testing.T) {
	sampleRate := 48000
	frameSize := 960
	freq := 440.0
	numFrames := 3

	// Generate signal
	original := make([]float64, frameSize*numFrames)
	for i := range original {
		ti := float64(i) / float64(sampleRate)
		original[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)

	t.Log("=== VBR Target Trace ===")
	t.Log("")

	for f := 0; f < numFrames; f++ {
		start := f * frameSize
		end := start + frameSize
		pcm := original[start:end]

		packet, err := enc.EncodeFrame(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode frame %d failed: %v", f, err)
		}

		t.Logf("Frame %d: %d bytes", f, len(packet))
	}
}
