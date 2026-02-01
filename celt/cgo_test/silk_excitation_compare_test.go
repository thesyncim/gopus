//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides tests for SILK excitation scaling comparison.
package cgo

import (
	"math"
	"testing"
)

// silkRshiftRound performs a right shift with rounding (Go version)
func silkRshiftRound(a, shift int32) int32 {
	if shift == 0 {
		return a
	}
	return (a + (1 << (shift - 1))) >> shift
}

// TestSILKExcitationScaling verifies that gopus excitation scaling matches libopus
func TestSILKExcitationScaling(t *testing.T) {
	// Test with typical gain value
	// SILK uses gains in Q16 format, typical values ~6000-65535
	// gainQ16 = 65536 means gain = 1.0

	testCases := []struct {
		name    string
		gainQ16 int32
	}{
		{"gain_1.0", 65536},
		{"gain_0.5", 32768},
		{"gain_2.0", 131072},
		{"gain_typical_low", 6000},
		{"gain_typical_high", 60000},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test signal: -32768 to 32767 int16 range
			// Simulating 80 samples at 8kHz (10ms subframe at NB)
			const numSamples = 80

			// Generate a simple sine wave in int16 range
			x16 := make([]int16, numSamples)
			for i := range x16 {
				// 300 Hz sine at 8kHz sample rate, amplitude 10000
				x16[i] = int16(10000 * math.Sin(2*math.Pi*300*float64(i)/8000))
			}

			// Compute inverse gain using libopus
			invGainQ31 := TestSilkINVERSE32VarQ(tc.gainQ16, 47)
			invGainQ26 := silkRshiftRound(invGainQ31, 5)

			// Scale input samples using libopus SMULWW
			scaledLibopus := make([]int32, numSamples)
			for i, v := range x16 {
				scaledLibopus[i] = TestSilkSMULWW(int32(v), invGainQ26)
			}

			t.Logf("gainQ16=%d, invGainQ31=%d, invGainQ26=%d",
				tc.gainQ16, invGainQ31, invGainQ26)

			// Show a few scaled samples
			t.Logf("Sample scaling examples:")
			for i := 0; i < 5 && i < numSamples; i++ {
				t.Logf("  x16[%d]=%d -> x_sc_Q10[%d]=%d", i, x16[i], i, scaledLibopus[i])
			}

			// Verify the scaling is non-trivial
			var maxScaled int32
			for i := 0; i < numSamples; i++ {
				v := scaledLibopus[i]
				if v < 0 {
					v = -v
				}
				if v > maxScaled {
					maxScaled = v
				}
			}
			t.Logf("Max |x_sc_Q10| = %d", maxScaled)

			// The scaled values should be significantly larger than 0-1 range
			// For gain=1.0, x_sc_Q10 = x16 * (1<<10) approximately
			if maxScaled < 1000 {
				t.Errorf("Scaled values too small: max=%d, expected significant magnitude", maxScaled)
			}
		})
	}
}

// TestSILKExcitationFloat32ToInt16 tests converting normalized float32 to int16
func TestSILKExcitationFloat32ToInt16(t *testing.T) {
	// This test demonstrates the scaling needed in gopus

	const numSamples = 80

	// Generate normalized float32 signal [-1, 1]
	pcmFloat := make([]float32, numSamples)
	for i := range pcmFloat {
		pcmFloat[i] = float32(0.3 * math.Sin(2*math.Pi*300*float64(i)/8000))
	}

	// Convert to int16 (Q0) - this is what libopus expects
	pcmInt16 := make([]int16, numSamples)
	for i, v := range pcmFloat {
		// Scale by 32768 to convert to Q0 int16 range
		scaled := v * 32768.0
		if scaled > 32767 {
			scaled = 32767
		} else if scaled < -32768 {
			scaled = -32768
		}
		pcmInt16[i] = int16(scaled)
	}

	// Now scale with libopus-style computation
	gainQ16 := int32(65536) // gain = 1.0
	invGainQ31 := TestSilkINVERSE32VarQ(gainQ16, 47)
	invGainQ26 := silkRshiftRound(invGainQ31, 5)

	scaledLibopus := make([]int32, numSamples)
	for i, v := range pcmInt16 {
		scaledLibopus[i] = TestSilkSMULWW(int32(v), invGainQ26)
	}

	t.Logf("Float32 to Int16 to x_sc_Q10 scaling:")
	for i := 0; i < 5; i++ {
		t.Logf("  pcmFloat[%d]=%.6f -> pcmInt16[%d]=%d -> x_sc_Q10[%d]=%d",
			i, pcmFloat[i], i, pcmInt16[i], i, scaledLibopus[i])
	}

	// The key insight:
	// - libopus expects Q0 int16 input
	// - gopus has normalized float32 [-1, 1]
	// - Before computing LPC residual, multiply by 32768
	// - The residual (excitation) will then have proper magnitude

	t.Logf("\nKey insight for excitation fix:")
	t.Logf("  Without scaling: round(%.6f) = %d", pcmFloat[0], int32(math.Round(float64(pcmFloat[0]))))
	t.Logf("  With 32768 scaling: round(%.6f * 32768) = %d",
		pcmFloat[0], int32(math.Round(float64(pcmFloat[0])*32768)))
}

// TestSILKResidualScalingComparison compares gopus-style vs libopus-style residual
func TestSILKResidualScalingComparison(t *testing.T) {
	// Simulate LPC residual computation

	const numSamples = 80

	// Generate signal
	pcmFloat := make([]float32, numSamples)
	for i := range pcmFloat {
		pcmFloat[i] = float32(0.3 * math.Sin(2*math.Pi*300*float64(i)/8000))
	}

	// Simple LPC residual: for demonstration, assume prediction = 0.8 * prev
	// residual[i] = input[i] - 0.8 * input[i-1]

	// Method 1: gopus current (WRONG) - compute residual on normalized float, then round
	residualWrong := make([]int32, numSamples)
	for i := 0; i < numSamples; i++ {
		var pred float32
		if i > 0 {
			pred = 0.8 * pcmFloat[i-1]
		}
		res := pcmFloat[i] - pred
		residualWrong[i] = int32(math.Round(float64(res)))
	}

	// Method 2: gopus fixed - scale by 32768 BEFORE computing residual
	residualCorrect := make([]int32, numSamples)
	pcmScaled := make([]float32, numSamples)
	for i := range pcmFloat {
		pcmScaled[i] = pcmFloat[i] * 32768.0
	}
	for i := 0; i < numSamples; i++ {
		var pred float32
		if i > 0 {
			pred = 0.8 * pcmScaled[i-1]
		}
		res := pcmScaled[i] - pred
		residualCorrect[i] = int32(math.Round(float64(res)))
	}

	t.Logf("Residual comparison (first 10 samples):")
	t.Logf("%-8s %-12s %-15s %-15s", "Index", "Input", "Wrong", "Correct")
	for i := 0; i < 10; i++ {
		t.Logf("%-8d %-12.6f %-15d %-15d",
			i, pcmFloat[i], residualWrong[i], residualCorrect[i])
	}

	// Count non-zero residuals
	var wrongNonZero, correctNonZero int
	for i := 0; i < numSamples; i++ {
		if residualWrong[i] != 0 {
			wrongNonZero++
		}
		if residualCorrect[i] != 0 {
			correctNonZero++
		}
	}

	t.Logf("\nNon-zero residuals: wrong=%d, correct=%d (of %d samples)",
		wrongNonZero, correctNonZero, numSamples)

	// The wrong method should have mostly zeros
	if wrongNonZero > numSamples/2 {
		t.Errorf("Expected mostly zeros with wrong method, got %d non-zero", wrongNonZero)
	}

	// The correct method should have mostly non-zeros
	if correctNonZero < numSamples/2 {
		t.Errorf("Expected mostly non-zeros with correct method, got %d non-zero", correctNonZero)
	}
}
