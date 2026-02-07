//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/encoder"
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
	pulses, yy := celt.OpPVQSearchExport(x, k)

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
	SetLibopusDebugRange(false)

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
	enc.SetBandwidth(gopus.BandwidthFullband)
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

	// CELT can introduce a small alignment offset on the first frame.
	// Use lag-compensated correlation to measure polarity/shape reliably.
	corr, bestLag := maxLagCorrelation(pcm, decoded, 120)

	t.Logf("Correlation with original: %.4f (best lag=%d)", corr, bestLag)

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

// TestEncoderOutputCorrelationAfterWarmup validates encoder output shape after
// one-frame startup effects (lookahead/window overlap) have settled.
func TestEncoderOutputCorrelationAfterWarmup(t *testing.T) {
	SetLibopusDebugRange(false)

	sampleRate := 48000
	frameSize := 960

	pcm := make([]float64, frameSize*2)
	for i := 0; i < len(pcm); i++ {
		ti := float64(i) / float64(sampleRate)
		pcm[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	enc := encoder.NewEncoder(48000, 1)
	enc.SetMode(encoder.ModeCELT)
	enc.SetBandwidth(gopus.BandwidthFullband)
	enc.SetBitrate(64000)

	pkt1, err := enc.Encode(pcm[:frameSize], frameSize)
	if err != nil {
		t.Fatalf("frame1 encode failed: %v", err)
	}
	pkt2, err := enc.Encode(pcm[frameSize:], frameSize)
	if err != nil {
		t.Fatalf("frame2 encode failed: %v", err)
	}

	libDec, err := NewLibopusDecoder(48000, 1)
	if err != nil {
		t.Fatalf("NewLibopusDecoder failed: %v", err)
	}
	defer libDec.Destroy()

	decoded1, samples1 := libDec.DecodeFloat(pkt1, frameSize)
	if samples1 <= 0 {
		t.Fatalf("frame1 decode failed: %d", samples1)
	}
	decoded2, samples2 := libDec.DecodeFloat(pkt2, frameSize)
	if samples2 <= 0 {
		t.Fatalf("frame2 decode failed: %d", samples2)
	}

	corr1, lag1 := maxLagCorrelation(pcm[:frameSize], decoded1, 120)
	corr2, lag2 := maxLagCorrelation(pcm[frameSize:], decoded2, 120)

	t.Logf("Frame1 correlation: %.4f (lag=%d)", corr1, lag1)
	t.Logf("Frame2 correlation: %.4f (lag=%d)", corr2, lag2)

	if corr2 < 0 {
		t.Errorf("frame2 signal inverted: corr=%.4f", corr2)
	} else if corr2 < 0.9 {
		t.Errorf("frame2 low correlation: %.4f (expected > 0.9)", corr2)
	}
}

func maxLagCorrelation(original []float64, decoded []float32, maxLag int) (bestCorr float64, bestLag int) {
	bestCorr = -1.0
	if maxLag < 0 {
		maxLag = 0
	}

	n := len(original)
	if len(decoded) < n {
		n = len(decoded)
	}
	if n <= 4 {
		return 0, 0
	}

	if maxLag >= n {
		maxLag = n - 1
	}

	for lag := 0; lag <= maxLag; lag++ {
		count := n - lag
		if count <= 4 {
			break
		}

		var sumOrig, sumDec, sumOrigDec float64
		var sumOrigSq, sumDecSq float64
		for i := 0; i < count; i++ {
			o := original[i]
			d := float64(decoded[i+lag])
			sumOrig += o
			sumDec += d
			sumOrigDec += o * d
			sumOrigSq += o * o
			sumDecSq += d * d
		}

		nf := float64(count)
		num := nf*sumOrigDec - sumOrig*sumDec
		den := math.Sqrt((nf*sumOrigSq - sumOrig*sumOrig) * (nf*sumDecSq - sumDec*sumDec))
		corr := 0.0
		if den > 0 {
			corr = num / den
		}

		if corr > bestCorr {
			bestCorr = corr
			bestLag = lag
		}
	}

	if bestCorr < -1.0 {
		bestCorr = 0
	}
	return bestCorr, bestLag
}
