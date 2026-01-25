package testvectors

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

func TestDebugRoundTrip(t *testing.T) {
	// Generate 1 frame of simple sine wave
	sampleRate := 48000
	frameSize := 960
	freq := 440.0

	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*freq*ti)
	}

	// Create CELT encoder
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)

	// Encode
	packet, err := enc.EncodeFrame(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	t.Logf("Encoded packet: %d bytes", len(packet))

	// Create CELT decoder
	dec := celt.NewDecoder(1)

	// Decode
	decoded, err := dec.DecodeFrame(packet, frameSize)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	t.Logf("Decoded samples: %d", len(decoded))

	// Compare
	maxOrig, maxDec := 0.0, 0.0
	maxDecIdx := 0
	for _, v := range pcm {
		if math.Abs(v) > maxOrig {
			maxOrig = math.Abs(v)
		}
	}
	for i, v := range decoded {
		if math.Abs(v) > maxDec {
			maxDec = math.Abs(v)
			maxDecIdx = i
		}
	}
	t.Logf("Max amplitudes: orig=%.4f, decoded=%.4f (at idx %d), ratio=%.2f",
		maxOrig, maxDec, maxDecIdx, maxDec/maxOrig)

	// Print samples around peak
	t.Log("\nSamples around decoded peak:")
	for i := maxDecIdx - 5; i <= maxDecIdx+5; i++ {
		if i >= 0 && i < len(decoded) && i < len(pcm) {
			t.Logf("  [%d] orig=%.4f, decoded=%.4f", i, pcm[i], decoded[i])
		}
	}

	// Print samples around index 480 (middle of frame, where sine peak is)
	t.Log("\nSamples around middle of frame (where original peaks):")
	for i := 235; i <= 245; i++ {
		if i >= 0 && i < len(decoded) && i < len(pcm) {
			t.Logf("  [%d] orig=%.4f, decoded=%.4f", i, pcm[i], decoded[i])
		}
	}
}
