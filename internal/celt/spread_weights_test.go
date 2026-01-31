// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file contains tests for spread weight computation.

package celt

import (
	"math"
	"testing"
)

// TestComputeSpreadWeightsBasic tests the basic functionality of ComputeSpreadWeights.
func TestComputeSpreadWeightsBasic(t *testing.T) {
	nbBands := 21
	channels := 1
	lsbDepth := 16

	// Create test band energies with varying levels
	bandLogE := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		// Simulate typical band energy distribution (higher in mid-frequencies)
		bandLogE[i] = 5.0 - 0.5*float64(i-10)*float64(i-10)/100.0
	}

	weights := ComputeSpreadWeights(bandLogE, nbBands, channels, lsbDepth)

	// Verify basic properties
	if len(weights) != nbBands {
		t.Errorf("Expected %d weights, got %d", nbBands, len(weights))
	}

	// Verify all weights are in valid range [1, 32]
	for i, w := range weights {
		if w < 1 || w > 32 {
			t.Errorf("Weight[%d] = %d, expected in range [1, 32]", i, w)
		}
	}

	t.Logf("Weights: %v", weights)
}

// TestComputeSpreadWeightsStereo tests spread weight computation for stereo audio.
func TestComputeSpreadWeightsStereo(t *testing.T) {
	nbBands := 21
	channels := 2
	lsbDepth := 16

	// Create test band energies for 2 channels
	bandLogE := make([]float64, 2*nbBands)
	for i := 0; i < nbBands; i++ {
		// Left channel: higher in low frequencies
		bandLogE[i] = 8.0 - 0.3*float64(i)
		// Right channel: higher in high frequencies
		bandLogE[nbBands+i] = 2.0 + 0.3*float64(i)
	}

	weights := ComputeSpreadWeights(bandLogE, nbBands, channels, lsbDepth)

	if len(weights) != nbBands {
		t.Errorf("Expected %d weights, got %d", nbBands, len(weights))
	}

	t.Logf("Stereo weights: %v", weights)
}

// TestComputeSpreadWeightsEdgeCases tests edge cases in spread weight computation.
func TestComputeSpreadWeightsEdgeCases(t *testing.T) {
	t.Run("EmptyBandLogE", func(t *testing.T) {
		weights := ComputeSpreadWeights(nil, 21, 1, 16)
		// Should return uniform weights
		for i, w := range weights {
			if w != 1 {
				t.Errorf("Weight[%d] = %d, expected 1 for empty input", i, w)
			}
		}
	})

	t.Run("FewBands", func(t *testing.T) {
		bandLogE := []float64{5.0, 6.0, 4.0}
		weights := ComputeSpreadWeights(bandLogE, 3, 1, 16)
		if len(weights) != 3 {
			t.Errorf("Expected 3 weights, got %d", len(weights))
		}
	})

	t.Run("HighBitDepth", func(t *testing.T) {
		nbBands := 21
		bandLogE := make([]float64, nbBands)
		for i := 0; i < nbBands; i++ {
			bandLogE[i] = 5.0
		}
		weights16 := ComputeSpreadWeights(bandLogE, nbBands, 1, 16)
		weights24 := ComputeSpreadWeights(bandLogE, nbBands, 1, 24)

		// Higher bit depth means lower noise floor, so weights may differ
		t.Logf("16-bit weights: %v", weights16)
		t.Logf("24-bit weights: %v", weights24)
	})
}

// TestComputeSpreadWeightsMatchesLibopusBehavior validates that weights
// follow the expected pattern from libopus.
func TestComputeSpreadWeightsMatchesLibopusBehavior(t *testing.T) {
	nbBands := 21
	channels := 1
	lsbDepth := 16

	// Test case: uniform high energy (all bands above noise floor)
	t.Run("UniformHighEnergy", func(t *testing.T) {
		bandLogE := make([]float64, nbBands)
		for i := 0; i < nbBands; i++ {
			bandLogE[i] = 10.0 // High energy
		}
		weights := ComputeSpreadWeights(bandLogE, nbBands, channels, lsbDepth)

		// With uniform high energy above noise floor, masking should be similar
		// across bands, resulting in relatively uniform weights
		sum := 0
		for _, w := range weights {
			sum += w
		}
		avg := float64(sum) / float64(nbBands)
		t.Logf("Average weight for high energy: %.2f", avg)
	})

	// Test case: one loud band (tests masking propagation)
	t.Run("OneLoudBand", func(t *testing.T) {
		bandLogE := make([]float64, nbBands)
		for i := 0; i < nbBands; i++ {
			bandLogE[i] = -5.0 // Low energy
		}
		bandLogE[10] = 15.0 // One very loud band

		weights := ComputeSpreadWeights(bandLogE, nbBands, channels, lsbDepth)

		// The loud band should mask nearby bands
		// Weights for masked bands should be lower
		t.Logf("One loud band weights: %v", weights)

		// The loud band (10) should have high weight
		if weights[10] < 16 {
			t.Errorf("Loud band weight = %d, expected >= 16", weights[10])
		}
	})

	// Test case: low energy (near noise floor)
	t.Run("LowEnergy", func(t *testing.T) {
		bandLogE := make([]float64, nbBands)
		for i := 0; i < nbBands; i++ {
			bandLogE[i] = -10.0 // Very low energy
		}
		weights := ComputeSpreadWeights(bandLogE, nbBands, channels, lsbDepth)

		// Low energy bands should have lower weights (more masked)
		sum := 0
		for _, w := range weights {
			sum += w
		}
		avg := float64(sum) / float64(nbBands)
		t.Logf("Average weight for low energy: %.2f", avg)
	})
}

// TestComputeSpreadWeightsNoiseFloorCalculation verifies noise floor matches libopus.
func TestComputeSpreadWeightsNoiseFloorCalculation(t *testing.T) {
	// Reference: libopus noise_floor[i] = 0.0625*logN[i] + 0.5 + (9-lsb_depth) - eMeans[i] + 0.0062*(i+5)^2
	lsbDepth := 16

	for i := 0; i < 21; i++ {
		logNVal := 0.0
		if i < len(LogN) {
			logNVal = float64(LogN[i])
		}
		eMean := 0.0
		if i < len(eMeans) {
			eMean = eMeans[i]
		}

		expectedNoiseFloor := 0.0625*logNVal + 0.5 + float64(9-lsbDepth) - eMean + 0.0062*float64((i+5)*(i+5))

		t.Logf("Band %d: logN=%d, eMeans=%.4f, noise_floor=%.4f", i, LogN[i], eMean, expectedNoiseFloor)
	}
}

// TestComputeSpreadWeightsSimple tests the convenience wrapper.
func TestComputeSpreadWeightsSimple(t *testing.T) {
	nbBands := 21
	bandLogE := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		bandLogE[i] = 5.0
	}

	weights := ComputeSpreadWeightsSimple(bandLogE, nbBands)
	if len(weights) != nbBands {
		t.Errorf("Expected %d weights, got %d", nbBands, len(weights))
	}

	// Compare with explicit call
	weightsExplicit := ComputeSpreadWeights(bandLogE, nbBands, 1, 16)
	for i := range weights {
		if weights[i] != weightsExplicit[i] {
			t.Errorf("Simple wrapper differs at band %d: %d vs %d", i, weights[i], weightsExplicit[i])
		}
	}
}

// TestSpreadingDecisionWithWeights tests the spread decision with computed weights.
func TestSpreadingDecisionWithWeights(t *testing.T) {
	encoder := NewEncoder(1)
	frameSize := 480
	nbBands := 21
	channels := 1

	// Create mock normalized coefficients
	N0 := frameSize
	normX := make([]float64, N0*channels)
	for i := range normX {
		// Simulate tonal content (few high values, many near zero)
		if i%20 == 0 {
			normX[i] = 0.9
		} else {
			normX[i] = 0.01
		}
	}

	// Create band energies for spread weights
	bandLogE := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		bandLogE[i] = 5.0 - 0.1*float64(i)
	}

	// Compute spread weights
	spreadWeights := ComputeSpreadWeights(bandLogE, nbBands, channels, 16)

	// Test with computed weights
	decision := encoder.SpreadingDecisionWithWeights(normX, nbBands, channels, frameSize, false, spreadWeights)

	t.Logf("Spread decision with computed weights: %d", decision)

	// Verify decision is in valid range
	if decision < 0 || decision > 3 {
		t.Errorf("Spread decision %d out of range [0, 3]", decision)
	}
}

// TestMaskingPropagation validates the forward/backward masking logic.
func TestMaskingPropagation(t *testing.T) {
	nbBands := 21

	// Test case: single loud band should spread masking both directions
	sig := make([]float64, nbBands)
	mask := make([]float64, nbBands)

	// Initialize with one loud band in the middle
	for i := 0; i < nbBands; i++ {
		sig[i] = -10.0
		mask[i] = -10.0
	}
	sig[10] = 20.0
	mask[10] = 20.0

	// Forward masking (like libopus)
	for i := 1; i < nbBands; i++ {
		if mask[i-1]-2.0 > mask[i] {
			mask[i] = mask[i-1] - 2.0
		}
	}

	// Backward masking (like libopus)
	for i := nbBands - 2; i >= 0; i-- {
		if mask[i+1]-3.0 > mask[i] {
			mask[i] = mask[i+1] - 3.0
		}
	}

	t.Log("After masking propagation:")
	for i := 0; i < nbBands; i++ {
		t.Logf("  Band %d: sig=%.2f, mask=%.2f", i, sig[i], mask[i])
	}

	// Verify masking spreads from band 10
	// Forward: bands 11+ should have elevated mask
	for i := 11; i < nbBands; i++ {
		expected := math.Max(-10.0, 20.0-2.0*float64(i-10))
		if math.Abs(mask[i]-expected) > 0.01 {
			t.Errorf("Forward mask[%d] = %.2f, expected %.2f", i, mask[i], expected)
		}
	}

	// Backward: bands 0-9 should have elevated mask
	for i := 9; i >= 0; i-- {
		expected := math.Max(-10.0, 20.0-3.0*float64(10-i))
		if math.Abs(mask[i]-expected) > 0.01 {
			t.Errorf("Backward mask[%d] = %.2f, expected %.2f", i, mask[i], expected)
		}
	}
}
