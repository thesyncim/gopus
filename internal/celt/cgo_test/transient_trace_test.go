// Package cgo traces transient detection between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTraceTransientDetection compares transient detection metrics.
func TestTraceTransientDetection(t *testing.T) {
	frameSize := 960
	sampleRate := 48000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== Transient Detection Trace ===")
	t.Log("")

	// Create gopus encoder
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)

	// Apply pre-emphasis (as done in encode_frame.go)
	preemph := enc.ApplyPreemphasisWithScaling(pcm64)

	// Build transient input with overlap (as done in encode_frame.go)
	overlap := celt.Overlap
	if overlap > frameSize {
		overlap = frameSize
	}

	// For first frame, previous overlap is zeros
	transientInput := make([]float64, overlap+frameSize)
	// First 'overlap' samples are zeros (no previous frame)
	copy(transientInput[overlap:], preemph)

	t.Logf("Frame size: %d, Overlap: %d, Total samples: %d", frameSize, overlap, len(transientInput))
	t.Logf("First 5 transient input samples: %.6f, %.6f, %.6f, %.6f, %.6f",
		transientInput[0], transientInput[1], transientInput[2], transientInput[3], transientInput[4])
	t.Logf("At overlap boundary [%d-%d]: %.6f, %.6f, %.6f",
		overlap-1, overlap+1, transientInput[overlap-1], transientInput[overlap], transientInput[overlap+1])
	t.Log("")

	// Run gopus transient analysis
	result := enc.TransientAnalysis(transientInput, frameSize+overlap, false)

	t.Log("=== Gopus Transient Analysis ===")
	t.Logf("IsTransient: %v", result.IsTransient)
	t.Logf("MaskMetric: %.2f (threshold: 200)", result.MaskMetric)
	t.Logf("TfEstimate: %.4f", result.TfEstimate)
	t.Logf("TfChannel: %d", result.TfChannel)
	t.Logf("WeakTransient: %v", result.WeakTransient)
	t.Log("")

	// Analyze why transient might not be detected
	if !result.IsTransient {
		if result.MaskMetric > 100 {
			t.Logf("MaskMetric is close to threshold (%.1f > 100), marginal case", result.MaskMetric)
		} else if result.MaskMetric > 50 {
			t.Logf("MaskMetric is moderate (%.1f), some temporal variation", result.MaskMetric)
		} else {
			t.Logf("MaskMetric is low (%.1f), steady signal", result.MaskMetric)
		}
	}

	// Test without the leading zeros (just the sine wave)
	t.Log("")
	t.Log("=== Testing Sine Wave Only (no leading zeros) ===")
	result2 := enc.TransientAnalysis(preemph, frameSize, false)
	t.Logf("IsTransient: %v", result2.IsTransient)
	t.Logf("MaskMetric: %.2f", result2.MaskMetric)

	// Test with a real transient: silence then sine
	t.Log("")
	t.Log("=== Testing Clear Transient: Silence -> Sine ===")
	transientSignal := make([]float64, 2*frameSize)
	// First half: silence
	// Second half: sine wave
	for i := frameSize; i < 2*frameSize; i++ {
		ti := float64(i-frameSize) / float64(sampleRate)
		transientSignal[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}
	// Apply pre-emphasis
	enc2 := celt.NewEncoder(1)
	transientPreemph := enc2.ApplyPreemphasisWithScaling(transientSignal)
	result3 := enc2.TransientAnalysis(transientPreemph, 2*frameSize, false)
	t.Logf("IsTransient: %v", result3.IsTransient)
	t.Logf("MaskMetric: %.2f", result3.MaskMetric)

	// Test with step function (clear transient)
	t.Log("")
	t.Log("=== Testing Step Function (0 -> 1) ===")
	stepSignal := make([]float64, frameSize+overlap)
	for i := overlap; i < frameSize+overlap; i++ {
		stepSignal[i] = 1.0
	}
	enc3 := celt.NewEncoder(1)
	result4 := enc3.TransientAnalysis(stepSignal, frameSize+overlap, false)
	t.Logf("IsTransient: %v", result4.IsTransient)
	t.Logf("MaskMetric: %.2f", result4.MaskMetric)
}
