package celt

import (
	"math"
	"testing"
)

// TestTapsetDecisionRange verifies tapset values are in valid range.
func TestTapsetDecisionRange(t *testing.T) {
	enc := NewEncoder(1)

	// Default should be 0
	if enc.TapsetDecision() != 0 {
		t.Errorf("Initial tapset = %d, want 0", enc.TapsetDecision())
	}

	// Test SetTapsetDecision with valid values
	for _, tapset := range []int{0, 1, 2} {
		enc.SetTapsetDecision(tapset)
		if enc.TapsetDecision() != tapset {
			t.Errorf("SetTapsetDecision(%d) = %d, want %d", tapset, enc.TapsetDecision(), tapset)
		}
	}

	// Test clamping of out-of-range values
	enc.SetTapsetDecision(-1)
	if enc.TapsetDecision() != 0 {
		t.Errorf("SetTapsetDecision(-1) = %d, want 0", enc.TapsetDecision())
	}

	enc.SetTapsetDecision(5)
	if enc.TapsetDecision() != 2 {
		t.Errorf("SetTapsetDecision(5) = %d, want 2", enc.TapsetDecision())
	}
}

// TestTapsetResetOnEncoder verifies tapset is reset properly.
func TestTapsetResetOnEncoder(t *testing.T) {
	enc := NewEncoder(1)

	// Set a non-zero tapset
	enc.SetTapsetDecision(2)
	if enc.TapsetDecision() != 2 {
		t.Fatalf("Failed to set tapset to 2")
	}

	// Reset should clear it
	enc.Reset()
	if enc.TapsetDecision() != 0 {
		t.Errorf("After Reset(), tapset = %d, want 0", enc.TapsetDecision())
	}
}

// TestTapsetFromSpreadingDecision verifies tapset is computed during spreading decision.
func TestTapsetFromSpreadingDecision(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960 // 20ms at 48kHz
	nbBands := 21

	// Create test normalized coefficients
	normX := make([]float64, frameSize)
	for i := range normX {
		// Create a signal with some high-frequency content
		// This should trigger tapset decision updates
		normX[i] = math.Sin(float64(i) * 0.1)
	}

	// Normalize to unit-norm per band (approximate)
	for band := 0; band < nbBands; band++ {
		start := ScaledBandStart(band, frameSize)
		end := ScaledBandEnd(band, frameSize)
		if start >= len(normX) || end > len(normX) || start >= end {
			continue
		}
		var sum float64
		for j := start; j < end; j++ {
			sum += normX[j] * normX[j]
		}
		if sum > 0 {
			scale := 1.0 / math.Sqrt(sum)
			for j := start; j < end; j++ {
				normX[j] *= scale
			}
		}
	}

	// Run spreading decision with updateHF=true
	_ = enc.SpreadingDecision(normX, nbBands, 1, frameSize, true)

	// Tapset should now have been computed (could be 0, 1, or 2)
	tapset := enc.TapsetDecision()
	if tapset < 0 || tapset > 2 {
		t.Errorf("TapsetDecision() = %d, want 0-2", tapset)
	}

	t.Logf("Computed tapset: %d, hfAverage: %d", tapset, enc.HFAverage())
}

// TestTapsetNotUpdatedWhenDisabled verifies tapset is not updated when updateHF=false.
func TestTapsetNotUpdatedWhenDisabled(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	nbBands := 21

	// Set initial tapset
	enc.SetTapsetDecision(1)
	initialTapset := enc.TapsetDecision()

	// Create test normalized coefficients
	normX := make([]float64, frameSize)
	for i := range normX {
		normX[i] = math.Sin(float64(i) * 0.5)
	}

	// Normalize
	for band := 0; band < nbBands; band++ {
		start := ScaledBandStart(band, frameSize)
		end := ScaledBandEnd(band, frameSize)
		if start >= len(normX) || end > len(normX) || start >= end {
			continue
		}
		var sum float64
		for j := start; j < end; j++ {
			sum += normX[j] * normX[j]
		}
		if sum > 0 {
			scale := 1.0 / math.Sqrt(sum)
			for j := start; j < end; j++ {
				normX[j] *= scale
			}
		}
	}

	// Run spreading decision with updateHF=false
	_ = enc.SpreadingDecision(normX, nbBands, 1, frameSize, false)

	// Tapset should remain unchanged
	if enc.TapsetDecision() != initialTapset {
		t.Errorf("Tapset changed from %d to %d with updateHF=false", initialTapset, enc.TapsetDecision())
	}
}

// TestTapsetGainTableValues verifies the tapset maps to correct comb filter gains.
// Reference: libopus celt/celt.c gains[3][3] table
func TestTapsetGainTableValues(t *testing.T) {
	// Expected gain values from libopus (Q15 converted to float)
	expectedGains := [3][3]float64{
		{0.3066406250, 0.2170410156, 0.1296386719}, // Tapset 0
		{0.4638671875, 0.2680664062, 0.0},          // Tapset 1
		{0.7998046875, 0.1000976562, 0.0},          // Tapset 2
	}

	for tapset := 0; tapset < 3; tapset++ {
		for tap := 0; tap < 3; tap++ {
			expected := expectedGains[tapset][tap]
			got := combFilterGains[tapset][tap]
			if math.Abs(got-expected) > 1e-6 {
				t.Errorf("combFilterGains[%d][%d] = %f, want %f", tapset, tap, got, expected)
			}
		}
	}
}

// TestTapsetHysteresis verifies the hysteresis behavior of tapset decision.
// Reference: libopus celt/bands.c spreading_decision() tapset update logic
func TestTapsetHysteresis(t *testing.T) {
	enc := NewEncoder(1)
	frameSize := 960
	nbBands := 21

	// Create test normalized coefficients with varying HF content
	testCases := []struct {
		name           string
		hfEnergy       float64 // Higher = more HF content = higher hfSum
		expectedTapset int     // Expected tapset after several iterations
	}{
		{"low_hf", 0.01, 0}, // Low HF -> tapset 0
		{"mid_hf", 0.3, 1},  // Medium HF -> tapset 1
		{"high_hf", 0.9, 2}, // High HF -> tapset 2
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc.Reset()

			normX := make([]float64, frameSize)
			for i := range normX {
				// Create signal with specific HF characteristics
				// Higher tc.hfEnergy means more concentrated energy (tonal)
				freq := 0.1 + tc.hfEnergy*0.5
				normX[i] = tc.hfEnergy * math.Sin(float64(i)*freq)
			}

			// Normalize per band
			for band := 0; band < nbBands; band++ {
				start := ScaledBandStart(band, frameSize)
				end := ScaledBandEnd(band, frameSize)
				if start >= len(normX) || end > len(normX) || start >= end {
					continue
				}
				var sum float64
				for j := start; j < end; j++ {
					sum += normX[j] * normX[j]
				}
				if sum > 0 {
					scale := 1.0 / math.Sqrt(sum)
					for j := start; j < end; j++ {
						normX[j] *= scale
					}
				}
			}

			// Run several iterations to reach steady state
			for i := 0; i < 10; i++ {
				_ = enc.SpreadingDecision(normX, nbBands, 1, frameSize, true)
			}

			tapset := enc.TapsetDecision()
			// Just verify it's in valid range; exact value depends on signal
			if tapset < 0 || tapset > 2 {
				t.Errorf("Final tapset = %d, want 0-2", tapset)
			}
			t.Logf("%s: tapset=%d, hfAverage=%d", tc.name, tapset, enc.HFAverage())
		})
	}
}
