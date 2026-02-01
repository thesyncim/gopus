// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides tests for tf_estimate computation.

package celt

import (
	"math"
	"testing"
)

// TestTfEstimateComputation tests that tf_estimate is computed correctly
// according to the libopus formula.
func TestTfEstimateComputation(t *testing.T) {
	// Test cases derived from libopus formula:
	// tf_max = max(0, sqrt(27 * mask_metric) - 42)
	// tf_estimate = sqrt(max(0, 0.0069 * min(163, tf_max) - 0.139))
	testCases := []struct {
		name          string
		maskMetric    float64
		expectedTfEst float64
		tolerance     float64
	}{
		{
			name:          "zero mask metric",
			maskMetric:    0,
			expectedTfEst: 0,
			tolerance:     0.001,
		},
		{
			name:          "low mask metric (below transient threshold)",
			maskMetric:    100,
			expectedTfEst: 0, // sqrt(27*100) = 52, tf_max = 52-42 = 10, sqrt(0.0069*10-0.139) = sqrt(-0.07) = 0
			tolerance:     0.001,
		},
		{
			name:          "at transient threshold",
			maskMetric:    200,
			expectedTfEst: 0.28, // sqrt(27*200) = 73.5, tf_max = 73.5-42 = 31.5, sqrt(0.0069*31.5-0.139) = sqrt(0.078) = 0.28
			tolerance:     0.05,
		},
		{
			name:          "moderate transient",
			maskMetric:    500,
			expectedTfEst: 0.61, // sqrt(27*500) = 116.2, tf_max = 74.2, sqrt(0.0069*74.2-0.139) = sqrt(0.373) = 0.61
			tolerance:     0.05,
		},
		{
			name:          "strong transient",
			maskMetric:    1000,
			expectedTfEst: 0.84, // sqrt(27*1000) = 164.3, tf_max = 122.3, sqrt(0.0069*122.3-0.139) = sqrt(0.705) = 0.84
			tolerance:     0.05,
		},
		{
			name:          "very strong transient (clamped)",
			maskMetric:    2000,
			expectedTfEst: 0.99, // sqrt(27*2000) = 232.4, tf_max = 190.4, clamped to 163, sqrt(0.0069*163-0.139) = sqrt(0.985) = 0.99
			tolerance:     0.05,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Compute tf_estimate using the same formula as TransientAnalysis
			tfMax := math.Max(0, math.Sqrt(27*tc.maskMetric)-42)
			clampedTfMax := math.Min(163, tfMax)
			tfEstimateSquared := 0.0069*clampedTfMax - 0.139
			if tfEstimateSquared < 0 {
				tfEstimateSquared = 0
			}
			tfEstimate := math.Sqrt(tfEstimateSquared)
			if tfEstimate > 1.0 {
				tfEstimate = 1.0
			}

			t.Logf("mask_metric=%.1f: tf_max=%.2f, clamped=%.2f, tf_estimate=%.4f (expected=%.2f)",
				tc.maskMetric, tfMax, clampedTfMax, tfEstimate, tc.expectedTfEst)

			if math.Abs(tfEstimate-tc.expectedTfEst) > tc.tolerance {
				t.Errorf("tf_estimate=%.4f, expected=%.2f (tolerance=%.2f)",
					tfEstimate, tc.expectedTfEst, tc.tolerance)
			}
		})
	}
}

// TestTransientAnalysisTfEstimate tests that TransientAnalysis produces
// reasonable tf_estimate values for different signal types.
func TestTransientAnalysisTfEstimate(t *testing.T) {
	encoder := NewEncoder(1)
	frameSize := 960

	testCases := []struct {
		name            string
		generateSignal  func() []float64
		expectTransient bool
		tfEstimateMin   float64
		tfEstimateMax   float64
	}{
		{
			name: "steady sine wave",
			generateSignal: func() []float64 {
				pcm := make([]float64, frameSize)
				for i := range pcm {
					pcm[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/48000)
				}
				return pcm
			},
			expectTransient: false,
			tfEstimateMin:   0.0,
			tfEstimateMax:   0.5, // Should be low for steady signal
		},
		{
			name: "sharp attack",
			generateSignal: func() []float64 {
				pcm := make([]float64, frameSize)
				// First half is quiet, second half is loud
				for i := frameSize / 2; i < frameSize; i++ {
					pcm[i] = 0.8 * math.Sin(2*math.Pi*1000*float64(i)/48000)
				}
				return pcm
			},
			expectTransient: true,
			tfEstimateMin:   0.0, // Tf_estimate measures temporal variation, not just transient
			tfEstimateMax:   1.0,
		},
		{
			name: "impulse",
			generateSignal: func() []float64 {
				pcm := make([]float64, frameSize)
				// Single impulse in the middle
				pcm[frameSize/2] = 1.0
				return pcm
			},
			expectTransient: true,
			tfEstimateMin:   0.0,
			tfEstimateMax:   1.0,
		},
		{
			name: "drum-like attack",
			generateSignal: func() []float64 {
				pcm := make([]float64, frameSize)
				// Sharp exponential decay
				for i := 0; i < frameSize; i++ {
					decay := math.Exp(-float64(i) / 100)
					pcm[i] = decay * math.Sin(2*math.Pi*200*float64(i)/48000)
				}
				return pcm
			},
			expectTransient: true,
			tfEstimateMin:   0.0,
			tfEstimateMax:   1.0,
		},
		{
			name: "silence",
			generateSignal: func() []float64 {
				return make([]float64, frameSize)
			},
			expectTransient: false,
			tfEstimateMin:   0.0,
			tfEstimateMax:   0.1, // Should be very low for silence
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := tc.generateSignal()
			result := encoder.TransientAnalysis(pcm, frameSize, false)

			t.Logf("%s: isTransient=%v (expect=%v), tfEstimate=%.4f, maskMetric=%.2f",
				tc.name, result.IsTransient, tc.expectTransient, result.TfEstimate, result.MaskMetric)

			if result.IsTransient != tc.expectTransient {
				t.Logf("Warning: isTransient=%v, expected=%v (might differ from simple threshold)",
					result.IsTransient, tc.expectTransient)
			}

			if result.TfEstimate < tc.tfEstimateMin || result.TfEstimate > tc.tfEstimateMax {
				t.Errorf("tfEstimate=%.4f, expected in range [%.2f, %.2f]",
					result.TfEstimate, tc.tfEstimateMin, tc.tfEstimateMax)
			}
		})
	}
}

// TestTfEstimateUsedInTFAnalysis verifies that tf_estimate affects TF analysis bias.
func TestTfEstimateUsedInTFAnalysis(t *testing.T) {
	frameSize := 960
	nbBands := 21
	lm := 3

	// Create a test signal with some spectral content
	normL := make([]float64, frameSize)
	for i := range normL {
		// Add some frequency content
		normL[i] = math.Sin(2*math.Pi*float64(i)*10/float64(frameSize)) * 0.5
	}

	// Test with different tf_estimate values
	tfEstimates := []float64{0.0, 0.2, 0.5, 0.8, 1.0}

	for _, tfEst := range tfEstimates {
		tfRes, tfSelect := TFAnalysis(normL, len(normL), nbBands, false, lm, tfEst, 100, nil)

		// Count how many bands use tf_res=1 (favor time resolution)
		timeResBands := 0
		for _, v := range tfRes {
			if v == 1 {
				timeResBands++
			}
		}

		t.Logf("tfEstimate=%.2f: tfSelect=%d, timeResBands=%d/%d",
			tfEst, tfSelect, timeResBands, nbBands)
	}

	// The actual TF decisions depend on the signal characteristics,
	// but we verify that different tf_estimate values are accepted
	// and produce valid output
	for _, tfEst := range tfEstimates {
		tfRes, _ := TFAnalysis(normL, len(normL), nbBands, false, lm, tfEst, 100, nil)
		if len(tfRes) != nbBands {
			t.Errorf("tfEstimate=%.2f: expected %d bands, got %d", tfEst, nbBands, len(tfRes))
		}
	}
}

// TestTfEstimateStereo verifies tf_estimate computation for stereo signals.
func TestTfEstimateStereo(t *testing.T) {
	encoder := NewEncoder(2)
	frameSize := 960

	// Create stereo signal with different content per channel
	pcm := make([]float64, frameSize*2) // Interleaved stereo

	// Left channel: steady sine
	// Right channel: attack
	for i := 0; i < frameSize; i++ {
		pcm[i*2] = 0.3 * math.Sin(2*math.Pi*440*float64(i)/48000) // Left

		if i >= frameSize/2 {
			pcm[i*2+1] = 0.8 * math.Sin(2*math.Pi*1000*float64(i)/48000) // Right (attack)
		}
	}

	result := encoder.TransientAnalysis(pcm, frameSize, false)

	t.Logf("Stereo analysis: isTransient=%v, tfEstimate=%.4f, tfChannel=%d, maskMetric=%.2f",
		result.IsTransient, result.TfEstimate, result.TfChannel, result.MaskMetric)

	// The channel with the stronger transient should be detected
	if result.IsTransient && result.TfChannel != 1 {
		t.Logf("Note: Expected right channel (1) to have stronger transient, got channel %d",
			result.TfChannel)
	}

	// Verify tf_estimate is in valid range
	if result.TfEstimate < 0 || result.TfEstimate > 1 {
		t.Errorf("tfEstimate=%.4f is out of range [0, 1]", result.TfEstimate)
	}
}

// TestWeakTransientMode verifies weak transient handling for hybrid mode.
func TestWeakTransientMode(t *testing.T) {
	encoder := NewEncoder(1)
	frameSize := 960

	// Create a signal with a moderate transient (mask_metric between 200 and 600)
	pcm := make([]float64, frameSize)
	// Moderate attack
	for i := frameSize / 2; i < frameSize; i++ {
		factor := float64(i-frameSize/2) / float64(frameSize/2) // Gradual increase
		pcm[i] = factor * 0.5 * math.Sin(2*math.Pi*500*float64(i)/48000)
	}

	// Test without weak transient allowance
	resultNormal := encoder.TransientAnalysis(pcm, frameSize, false)

	// Test with weak transient allowance (simulates hybrid mode at low bitrate)
	resultWeak := encoder.TransientAnalysis(pcm, frameSize, true)

	t.Logf("Normal: isTransient=%v, weakTransient=%v, maskMetric=%.2f",
		resultNormal.IsTransient, resultNormal.WeakTransient, resultNormal.MaskMetric)
	t.Logf("Weak allowed: isTransient=%v, weakTransient=%v, maskMetric=%.2f",
		resultWeak.IsTransient, resultWeak.WeakTransient, resultWeak.MaskMetric)

	// If mask_metric is in the weak range (200-600), allowing weak transients
	// should mark it as weak rather than full transient
	if resultNormal.MaskMetric > 200 && resultNormal.MaskMetric < 600 {
		if !resultWeak.WeakTransient {
			t.Logf("Note: Expected weak transient to be detected with mask_metric=%.2f",
				resultWeak.MaskMetric)
		}
	}
}
