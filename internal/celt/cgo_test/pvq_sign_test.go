package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/types"
)

// TestPVQSignPreservation tests if PVQ search preserves signs correctly
func TestPVQSignPreservation(t *testing.T) {
	// Create a simple test vector with known signs
	n := 8
	x := make([]float64, n)
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			x[i] = 0.5 // positive
		} else {
			x[i] = -0.5 // negative
		}
	}

	// Save original values
	original := make([]float64, n)
	copy(original, x)

	// Test opPVQSearch
	k := 4
	pulses, yy := celt.ExportedOpPVQSearch(x, k)

	t.Logf("Original input:  %v", original)
	t.Logf("After PVQ search: %v", x)
	t.Logf("Pulses:          %v", pulses)
	t.Logf("Energy yy:       %f", yy)

	// Check if x was modified
	modified := false
	for i := 0; i < n; i++ {
		if x[i] != original[i] {
			modified = true
			break
		}
	}

	if modified {
		t.Errorf("BUG: opPVQSearch modified input slice in place!")
		t.Logf("Expected: %v", original)
		t.Logf("Got:      %v", x)
	}

	// Check pulse signs match input signs
	for i := 0; i < n; i++ {
		if pulses[i] != 0 {
			pulsePositive := pulses[i] > 0
			inputPositive := original[i] >= 0
			if pulsePositive != inputPositive {
				t.Errorf("Pulse sign mismatch at index %d: input=%.2f, pulse=%d",
					i, original[i], pulses[i])
			}
		}
	}
}

// TestEncoderOutputCorrelation tests if the encoder output has correct polarity
func TestEncoderOutputCorrelation(t *testing.T) {
	sampleRate := 48000
	frameSize := 960

	// Generate a simple sine wave
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm[i] = val
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
	libDec, err := NewLibopusDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	decoded, samples := libDec.DecodeFloat(gopusPacket, frameSize)
	if samples <= 0 {
		t.Fatalf("decode failed: %d", samples)
	}

	// Compute correlation between original and decoded
	var sumOrig, sumDec, sumOrigDec float64
	var sumOrigSq, sumDecSq float64
	for i := 0; i < frameSize; i++ {
		o := pcm[i]
		d := float64(decoded[i])
		sumOrig += o
		sumDec += d
		sumOrigDec += o * d
		sumOrigSq += o * o
		sumDecSq += d * d
	}
	n := float64(frameSize)
	num := n*sumOrigDec - sumOrig*sumDec
	den := math.Sqrt((n*sumOrigSq - sumOrig*sumOrig) * (n*sumDecSq - sumDec*sumDec))
	corr := 0.0
	if den > 0 {
		corr = num / den
	}

	t.Logf("Correlation with original: %.4f", corr)

	// Negative correlation means signal inversion
	if corr < 0 {
		t.Errorf("SIGNAL INVERTED! Correlation = %.4f (expected positive)", corr)
	} else if corr < 0.3 {
		t.Errorf("Low correlation: %.4f (expected > 0.3)", corr)
	}

	// Show first few samples for debugging
	t.Log("\nFirst 10 samples comparison:")
	t.Logf("  idx     original     decoded")
	for i := 0; i < 10; i++ {
		t.Logf("  [%d]  %10.5f  %10.5f", i, pcm[i], decoded[i])
	}
}
