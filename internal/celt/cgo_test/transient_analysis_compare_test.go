package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTransientAnalysisDebug debugs why transient detection differs
func TestTransientAnalysisDebug(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	overlap := celt.Overlap // 120

	// Generate 440Hz sine wave
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate))
	}

	// Create encoder and apply same preprocessing as EncodeFrame
	enc := celt.NewEncoder(1)
	enc.Reset()
	enc.SetBitrate(64000)

	t.Log("=== Transient Analysis Debug ===")
	t.Log("")

	// Step 1: DC rejection (as in EncodeFrame)
	dcRejected := enc.ApplyDCReject(samples)
	t.Logf("After DC reject: max=%.6f, min=%.6f", maxAbsF64(dcRejected), minAbsF64(dcRejected))

	// Step 2: Apply pre-emphasis with scaling
	// Note: EncodeFrame also has delay buffer handling which we skip for simplicity
	preemph := enc.ApplyPreemphasisWithScaling(dcRejected)
	t.Logf("After pre-emphasis: max=%.6f, min=%.6f", maxAbsF64(preemph), -minAbsF64(preemph))

	// Step 3: Build transient input (overlap + current frame)
	// For first frame, preemphBuffer is all zeros
	preemphBufSize := overlap // 120 for mono
	transientInput := make([]float64, overlap+frameSize)
	// First 120 samples are zeros (from empty preemphBuffer)
	copy(transientInput[preemphBufSize:], preemph)

	t.Logf("")
	t.Logf("Transient input:")
	t.Logf("  Total length: %d samples (overlap=%d + frame=%d)", len(transientInput), overlap, frameSize)
	t.Logf("  First %d samples (overlap buffer): all zeros", overlap)
	t.Logf("  Samples %d-%d (frame start):", overlap, overlap+10)
	for i := overlap; i < overlap+10; i++ {
		t.Logf("    [%d] = %.6f", i, transientInput[i])
	}

	// Step 4: Call transient analysis
	result := enc.TransientAnalysis(transientInput, frameSize+overlap, false)

	t.Logf("")
	t.Logf("TransientAnalysis result:")
	t.Logf("  IsTransient: %v", result.IsTransient)
	t.Logf("  MaskMetric: %.2f (threshold=200)", result.MaskMetric)
	t.Logf("  TfEstimate: %.6f", result.TfEstimate)
	t.Logf("  ToneFreq: %.6f (radians/sample)", result.ToneFreq)
	t.Logf("  Toneishness: %.6f", result.Toneishness)

	// Analyze why mask_metric might be low
	t.Logf("")
	t.Logf("Analysis:")
	if result.MaskMetric < 200 {
		t.Logf("  mask_metric < 200, so IsTransient=false")
		t.Logf("  This is different from libopus which has IsTransient=true")
		t.Logf("")
		t.Logf("  Possible causes:")
		t.Logf("  1. The overlap buffer (zeros) doesn't create same energy pattern")
		t.Logf("  2. High-pass filter state differs")
		t.Logf("  3. Energy computation differs")
	}

	// Check if the first frame has a significant energy jump
	// The pre-emphasis should create a spike because previous sample is 0
	t.Logf("")
	t.Logf("First sample analysis:")
	t.Logf("  preemph[0] = %.6f (should be large since prev=0)", preemph[0])
	t.Logf("  preemph[1] = %.6f", preemph[1])
	t.Logf("  samples[0] = %.6f", samples[0])
	t.Logf("  samples[1] = %.6f", samples[1])
}

func maxAbsF64(s []float64) float64 {
	m := 0.0
	for _, v := range s {
		if math.Abs(v) > m {
			m = math.Abs(v)
		}
	}
	return m
}

func minAbsF64(s []float64) float64 {
	m := math.Inf(1)
	for _, v := range s {
		if math.Abs(v) < m {
			m = math.Abs(v)
		}
	}
	return m
}
