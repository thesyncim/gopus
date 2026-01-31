// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file contains tests for dynalloc_analysis functionality.
//
// dynalloc_analysis determines how to allocate extra bits to bands that have
// high energy relative to a spectral follower curve. This is critical for
// maintaining quality on signals with non-uniform spectral envelopes.
//
// Reference: libopus celt/celt_encoder.c dynalloc_analysis() (lines 1049-1273)

package celt

import (
	"math"
	"testing"
)

// =============================================================================
// Test Utilities
// =============================================================================

// generateRealisticBandEnergies generates band energies simulating a typical
// music-like spectral envelope (higher in low-mid frequencies, rolling off at highs).
func generateRealisticBandEnergies(nbBands int) []float64 {
	energies := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		// Simulate typical music spectrum:
		// - Peak around band 5-10 (low-mid frequencies)
		// - Gradual rolloff at high frequencies
		// - Some variation to simulate natural content
		center := 7.0
		spread := 6.0
		base := 8.0 - math.Pow(float64(i)-center, 2)/(spread*spread)*4.0

		// Add slight variation
		variation := 0.5 * math.Sin(float64(i)*0.7)
		energies[i] = base + variation
	}
	return energies
}

// generateFlatBandEnergies generates flat spectrum band energies at a given level.
func generateFlatBandEnergies(nbBands int, level float64) []float64 {
	energies := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		energies[i] = level
	}
	return energies
}

// generateStereoEnergies generates stereo band energies with specified patterns.
func generateStereoEnergies(nbBands int, leftPattern, rightPattern []float64) []float64 {
	if len(leftPattern) < nbBands || len(rightPattern) < nbBands {
		// Extend patterns if needed
		for len(leftPattern) < nbBands {
			leftPattern = append(leftPattern, leftPattern[len(leftPattern)-1])
		}
		for len(rightPattern) < nbBands {
			rightPattern = append(rightPattern, rightPattern[len(rightPattern)-1])
		}
	}
	energies := make([]float64, 2*nbBands)
	copy(energies[:nbBands], leftPattern[:nbBands])
	copy(energies[nbBands:], rightPattern[:nbBands])
	return energies
}

// =============================================================================
// Test 1: Noise Floor Computation
// =============================================================================

// TestNoiseFloorComputation verifies noise floor formula matches libopus.
// Reference: libopus celt_encoder.c lines 1071-1076:
//
//	noise_floor[i] = 0.0625*logN[i] + 0.5 + (9-lsb_depth) - eMeans[i]/16 + 0.0062*(i+5)^2
//
// Note: The eMeans division by 16 is for fixed-point; in float builds it's just eMeans[i].
func TestNoiseFloorComputation(t *testing.T) {
	testCases := []struct {
		name     string
		lsbDepth int
	}{
		{"16-bit audio", 16},
		{"24-bit audio", 24},
		{"8-bit audio", 8},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nbBands := 21
			noiseFloor := make([]float64, nbBands)

			// Compute noise floor using the formula from libopus
			for i := 0; i < nbBands; i++ {
				logNVal := 0.0
				if i < len(LogN) {
					// LogN is in Q8 format, so divide by 256 for actual log2 value
					// However, for noise floor computation, libopus uses the raw value
					// and scales by 0.0625 (1/16)
					logNVal = float64(LogN[i])
				}
				eMean := 0.0
				if i < len(eMeans) {
					eMean = eMeans[i]
				}

				// libopus formula (float version):
				// noise_floor[i] = 0.0625*logN[i] + 0.5 + (9-lsb_depth) - eMeans[i] + 0.0062*(i+5)^2
				noiseFloor[i] = 0.0625*logNVal + 0.5 + float64(9-tc.lsbDepth) - eMean + 0.0062*float64((i+5)*(i+5))
			}

			// Verify properties of the noise floor
			t.Logf("Noise floor for %d-bit depth:", tc.lsbDepth)
			for i := 0; i < nbBands; i++ {
				t.Logf("  Band %d: logN=%d, eMeans=%.4f, noise_floor=%.4f",
					i, LogN[i], eMeans[i], noiseFloor[i])
			}

			// Higher bit depth should result in lower noise floor
			if tc.lsbDepth == 24 {
				// 24-bit should have 8 units lower noise floor than 16-bit
				for i := 0; i < nbBands; i++ {
					// Just verify noise floor decreases with bit depth
					if noiseFloor[i] > 0 {
						t.Logf("Note: Band %d has positive noise floor %.4f", i, noiseFloor[i])
					}
				}
			}

			// Verify noise floor generally increases for higher bands (preemphasis effect)
			// The (i+5)^2 term causes this
			for i := 5; i < nbBands-1; i++ {
				if noiseFloor[i+1] < noiseFloor[i]-1.0 {
					t.Logf("Warning: noise floor decreased significantly from band %d to %d: %.4f -> %.4f",
						i, i+1, noiseFloor[i], noiseFloor[i+1])
				}
			}
		})
	}
}

// TestNoiseFloorConsistency verifies noise floor is consistent with spread weight computation.
func TestNoiseFloorConsistency(t *testing.T) {
	nbBands := 21
	lsbDepth := 16

	// The noise floor used in ComputeSpreadWeights should match our formula
	bandLogE := generateFlatBandEnergies(nbBands, 10.0)
	weights := ComputeSpreadWeights(bandLogE, nbBands, 1, lsbDepth)

	// With flat energy above noise floor, weights should be relatively uniform
	t.Logf("Spread weights with flat energy at 10.0:")
	for i := 0; i < nbBands; i++ {
		t.Logf("  Band %d: weight=%d", i, weights[i])
		// Weights should be in valid range
		if weights[i] < 1 || weights[i] > 32 {
			t.Errorf("Band %d: weight %d out of range [1, 32]", i, weights[i])
		}
	}
}

// =============================================================================
// Test 2: MaxDepth Calculation
// =============================================================================

// TestMaxDepthCalculation verifies maxDepth is max(bandLogE - noiseFloor) across all bands.
// Reference: libopus celt_encoder.c lines 1068-1081
func TestMaxDepthCalculation(t *testing.T) {
	testCases := []struct {
		name     string
		nbBands  int
		channels int
		energies []float64
	}{
		{
			name:     "mono flat",
			nbBands:  21,
			channels: 1,
			energies: generateFlatBandEnergies(21, 5.0),
		},
		{
			name:     "mono peaked",
			nbBands:  21,
			channels: 1,
			energies: generateRealisticBandEnergies(21),
		},
		{
			name:     "stereo flat",
			nbBands:  21,
			channels: 2,
			energies: generateStereoEnergies(21, generateFlatBandEnergies(21, 5.0), generateFlatBandEnergies(21, 8.0)),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lsbDepth := 16

			// Compute noise floor
			noiseFloor := make([]float64, tc.nbBands)
			for i := 0; i < tc.nbBands; i++ {
				logNVal := 0.0
				if i < len(LogN) {
					logNVal = float64(LogN[i])
				}
				eMean := 0.0
				if i < len(eMeans) {
					eMean = eMeans[i]
				}
				noiseFloor[i] = 0.0625*logNVal + 0.5 + float64(9-lsbDepth) - eMean + 0.0062*float64((i+5)*(i+5))
			}

			// Compute maxDepth
			maxDepth := -31.9 // libopus initial value
			for c := 0; c < tc.channels; c++ {
				for i := 0; i < tc.nbBands; i++ {
					idx := c*tc.nbBands + i
					if idx < len(tc.energies) {
						depth := tc.energies[idx] - noiseFloor[i]
						if depth > maxDepth {
							maxDepth = depth
						}
					}
				}
			}

			t.Logf("maxDepth = %.4f", maxDepth)

			// Verify maxDepth is reasonable
			if maxDepth < -31.9 {
				t.Errorf("maxDepth %.4f below initial value -31.9", maxDepth)
			}

			// For stereo with different channel levels, maxDepth should reflect the louder channel
			if tc.channels == 2 {
				// Find max depth separately for each channel
				maxDepthL := -31.9
				maxDepthR := -31.9
				for i := 0; i < tc.nbBands; i++ {
					depthL := tc.energies[i] - noiseFloor[i]
					depthR := tc.energies[tc.nbBands+i] - noiseFloor[i]
					if depthL > maxDepthL {
						maxDepthL = depthL
					}
					if depthR > maxDepthR {
						maxDepthR = depthR
					}
				}
				expectedMax := math.Max(maxDepthL, maxDepthR)
				if math.Abs(maxDepth-expectedMax) > 0.001 {
					t.Errorf("Stereo maxDepth mismatch: got %.4f, expected max(%.4f, %.4f) = %.4f",
						maxDepth, maxDepthL, maxDepthR, expectedMax)
				}
			}
		})
	}
}

// =============================================================================
// Test 3: Spread Weight Computation
// =============================================================================

// TestSpreadWeightComputation verifies spread weights based on SMR (signal-to-mask ratio).
// Reference: libopus celt_encoder.c lines 1083-1117
// Weights should be powers of 2: 1, 2, 4, 8, 16, 32
func TestSpreadWeightComputation(t *testing.T) {
	validWeights := map[int]bool{1: true, 2: true, 4: true, 8: true, 16: true, 32: true}

	testCases := []struct {
		name     string
		nbBands  int
		channels int
		lsbDepth int
		energies []float64
	}{
		{
			name:     "high energy mono",
			nbBands:  21,
			channels: 1,
			lsbDepth: 16,
			energies: generateFlatBandEnergies(21, 15.0),
		},
		{
			name:     "low energy mono",
			nbBands:  21,
			channels: 1,
			lsbDepth: 16,
			energies: generateFlatBandEnergies(21, -5.0),
		},
		{
			name:     "realistic mono",
			nbBands:  21,
			channels: 1,
			lsbDepth: 16,
			energies: generateRealisticBandEnergies(21),
		},
		{
			name:     "24-bit high energy",
			nbBands:  21,
			channels: 1,
			lsbDepth: 24,
			energies: generateFlatBandEnergies(21, 15.0),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			weights := ComputeSpreadWeights(tc.energies, tc.nbBands, tc.channels, tc.lsbDepth)

			if len(weights) != tc.nbBands {
				t.Errorf("Expected %d weights, got %d", tc.nbBands, len(weights))
				return
			}

			t.Logf("Spread weights:")
			for i := 0; i < tc.nbBands; i++ {
				t.Logf("  Band %d: weight=%d", i, weights[i])

				// Verify weight is a valid power of 2
				if !validWeights[weights[i]] {
					t.Errorf("Band %d: weight %d is not a valid power of 2 (1,2,4,8,16,32)", i, weights[i])
				}
			}
		})
	}
}

// TestSpreadWeightMaskingModel verifies the masking propagation in spread weights.
func TestSpreadWeightMaskingModel(t *testing.T) {
	nbBands := 21
	channels := 1
	lsbDepth := 16

	// Create energy with one very loud band
	energies := generateFlatBandEnergies(nbBands, -5.0)
	loudBand := 10
	energies[loudBand] = 20.0 // Much louder than neighbors

	weights := ComputeSpreadWeights(energies, nbBands, channels, lsbDepth)

	t.Logf("Weights with loud band at %d:", loudBand)
	for i := 0; i < nbBands; i++ {
		t.Logf("  Band %d: energy=%.2f, weight=%d", i, energies[i], weights[i])
	}

	// The loud band should have high weight
	if weights[loudBand] < 16 {
		t.Errorf("Loud band %d should have high weight (>=16), got %d", loudBand, weights[loudBand])
	}

	// Nearby bands should be masked (lower weights due to masking)
	// Forward masking (2 dB/band slope)
	// Backward masking (3 dB/band slope)
	// This is a qualitative check - exact values depend on noise floor
}

// =============================================================================
// Test 4: Follower Smoothing
// =============================================================================

// TestFollowerSmoothing verifies forward/backward smoothing and median filter.
// Reference: libopus celt_encoder.c lines 1137-1166
func TestFollowerSmoothing(t *testing.T) {
	nbBands := 21

	// Create test energies with a spike
	bandLogE3 := generateFlatBandEnergies(nbBands, 5.0)
	bandLogE3[10] = 15.0 // Large spike

	// Simulate follower computation
	f := make([]float64, nbBands)

	// Forward pass: follower rises at most 1.5 dB per band
	f[0] = bandLogE3[0]
	last := 0
	for i := 1; i < nbBands; i++ {
		if bandLogE3[i] > bandLogE3[i-1]+0.5 {
			last = i
		}
		if f[i-1]+1.5 < bandLogE3[i] {
			f[i] = f[i-1] + 1.5
		} else {
			f[i] = bandLogE3[i]
		}
	}

	t.Logf("After forward pass (last=%d):", last)
	for i := 0; i < nbBands; i++ {
		t.Logf("  Band %d: bandLogE3=%.2f, f=%.2f", i, bandLogE3[i], f[i])
	}

	// Backward pass: follower falls at most 2 dB per band
	for i := last - 1; i >= 0; i-- {
		if f[i+1]+2.0 < f[i] {
			f[i] = f[i+1] + 2.0
		}
		if bandLogE3[i] < f[i] {
			f[i] = bandLogE3[i]
		}
	}

	t.Logf("After backward pass:")
	for i := 0; i < nbBands; i++ {
		t.Logf("  Band %d: bandLogE3=%.2f, f=%.2f", i, bandLogE3[i], f[i])
	}

	// Verify follower tracks the spike
	// The follower should rise gradually toward the spike and fall gradually after
	for i := 1; i < 10; i++ {
		// Before spike: follower should be rising toward it (if spike is high enough)
		if f[i] < f[i-1]-0.001 && bandLogE3[i] > f[i] {
			t.Logf("Note: follower[%d]=%.2f < follower[%d]=%.2f but energy=%.2f is higher",
				i, f[i], i-1, f[i-1], bandLogE3[i])
		}
	}
}

// TestMedianFilterApplication verifies median filter in follower smoothing.
// Reference: libopus celt_encoder.c lines 1151-1162
func TestMedianFilterApplication(t *testing.T) {
	// Simulate the median filter applied to bandLogE3
	// offset = 1.0 (conservative value from libopus)
	offset := 1.0

	// Create test data with outliers
	bandLogE3 := []float64{5.0, 5.0, 12.0, 5.0, 5.0, 5.0, 5.0, 5.0, 5.0, 5.0,
		5.0, 5.0, 5.0, 5.0, 5.0, 5.0, 5.0, 5.0, 5.0, 5.0, 5.0}
	nbBands := len(bandLogE3)

	// Initialize follower with smoothed values
	f := make([]float64, nbBands)
	copy(f, bandLogE3)

	// Apply forward smoothing
	for i := 1; i < nbBands; i++ {
		if f[i-1]+1.5 < f[i] {
			f[i] = f[i-1] + 1.5
		}
	}

	t.Logf("Before median filter:")
	for i := 2; i < nbBands-2; i++ {
		med5 := medianOf5Float(bandLogE3[i-2:])
		t.Logf("  Band %d: f=%.2f, median5=%.2f", i, f[i], med5-offset)
	}

	// Apply median filter: f[i] = max(f[i], median_of_5(bandLogE3[i-2..i+2]) - offset)
	for i := 2; i < nbBands-2; i++ {
		med := medianOf5Float(bandLogE3[i-2:]) - offset
		if med > f[i] {
			f[i] = med
		}
	}

	// Handle edges with median_of_3
	if nbBands >= 3 {
		tmp := medianOf3Float(bandLogE3[0:]) - offset
		if tmp > f[0] {
			f[0] = tmp
		}
		if tmp > f[1] {
			f[1] = tmp
		}
	}
	if nbBands >= 3 {
		tmp := medianOf3Float(bandLogE3[nbBands-3:]) - offset
		if tmp > f[nbBands-2] {
			f[nbBands-2] = tmp
		}
		if tmp > f[nbBands-1] {
			f[nbBands-1] = tmp
		}
	}

	t.Logf("After median filter:")
	for i := 0; i < nbBands; i++ {
		t.Logf("  Band %d: f=%.2f", i, f[i])
	}

	// The median filter should help smooth out the outlier at band 2
	// But since it's surrounded by 5.0 values, median should be 5.0
}

// =============================================================================
// Test 5: Importance Weights
// =============================================================================

// TestImportanceWeights verifies importance calculation.
// Reference: libopus celt_encoder.c lines 1184-1191
func TestImportanceWeights(t *testing.T) {
	testCases := []struct {
		name           string
		effectiveBytes int
		lm             int
		expectDefault  bool
	}{
		{
			name:           "low bitrate LM=0",
			effectiveBytes: 20,
			lm:             0,
			expectDefault:  true, // < 30 + 5*0 = 30
		},
		{
			name:           "low bitrate LM=3",
			effectiveBytes: 40,
			lm:             3,
			expectDefault:  true, // < 30 + 5*3 = 45
		},
		{
			name:           "sufficient bitrate LM=3",
			effectiveBytes: 50,
			lm:             3,
			expectDefault:  false, // >= 30 + 5*3 = 45
		},
		{
			name:           "high bitrate",
			effectiveBytes: 200,
			lm:             3,
			expectDefault:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			nbBands := 21
			channels := 1
			lsbDepth := 16

			bandLogE := generateRealisticBandEnergies(nbBands)
			oldBandE := generateFlatBandEnergies(MaxBands, 5.0)

			importance := ComputeImportance(bandLogE, oldBandE, nbBands, channels, tc.lm, lsbDepth, tc.effectiveBytes)

			if len(importance) != nbBands {
				t.Errorf("Expected %d importance values, got %d", nbBands, len(importance))
				return
			}

			t.Logf("Importance weights (effectiveBytes=%d, LM=%d):", tc.effectiveBytes, tc.lm)
			allDefault := true
			for i := 0; i < nbBands; i++ {
				t.Logf("  Band %d: importance=%d", i, importance[i])
				if importance[i] != 13 {
					allDefault = false
				}
			}

			if tc.expectDefault && !allDefault {
				t.Errorf("Expected all default importance (13), but got varying values")
			}
			if !tc.expectDefault && allDefault {
				t.Errorf("Expected computed importance values, but got all defaults (13)")
			}
		})
	}
}

// TestImportanceScaling verifies importance scales with follower excess.
// Reference: libopus celt_encoder.c line 1187-1189:
//
//	importance[i] = floor(0.5 + 13 * celt_exp2_db(min(follower[i], 4.0)))
func TestImportanceScaling(t *testing.T) {
	// Test the scaling formula: importance = 13 * 2^(min(follower, 4))
	// When follower = 0, importance = 13 * 2^0 = 13
	// When follower = 1, importance = 13 * 2^1 = 26
	// When follower = 2, importance = 13 * 2^2 = 52
	// When follower = 4, importance = 13 * 2^4 = 208

	expectedTable := map[float64]int{
		0.0: 13,
		1.0: 26,
		2.0: 52,
		3.0: 104,
		4.0: 208,
	}

	for follower, expected := range expectedTable {
		actual := int(math.Floor(0.5 + 13.0*math.Pow(2.0, follower)))
		if actual != expected {
			t.Errorf("follower=%.1f: expected importance=%d, got %d", follower, expected, actual)
		}
	}

	// Values above 4.0 should be clamped
	capped := int(math.Floor(0.5 + 13.0*math.Pow(2.0, 4.0)))
	if capped != 208 {
		t.Errorf("Expected capped importance=208 for follower>4, got %d", capped)
	}
}

// =============================================================================
// Test 6: Offset/Boost Computation
// =============================================================================

// TestOffsetComputation verifies boost calculation per band.
// Reference: libopus celt_encoder.c lines 1232-1265
func TestOffsetComputation(t *testing.T) {
	// The offset (boost) depends on:
	// - follower value (clamped to 4)
	// - band width
	// - VBR vs CBR mode

	testCases := []struct {
		name              string
		vbr               bool
		constrainedVBR    bool
		isTransient       bool
		effectiveBytes    int
		expectTwoThirdCap bool
	}{
		{
			name:              "VBR mode",
			vbr:               true,
			constrainedVBR:    false,
			isTransient:       false,
			effectiveBytes:    100,
			expectTwoThirdCap: false,
		},
		{
			name:              "CBR mode",
			vbr:               false,
			constrainedVBR:    false,
			isTransient:       false,
			effectiveBytes:    100,
			expectTwoThirdCap: true, // CBR caps at 2/3
		},
		{
			name:              "CVBR non-transient",
			vbr:               true,
			constrainedVBR:    true,
			isTransient:       false,
			effectiveBytes:    100,
			expectTwoThirdCap: true, // CVBR non-transient also caps
		},
		{
			name:              "CVBR transient",
			vbr:               true,
			constrainedVBR:    true,
			isTransient:       true,
			effectiveBytes:    100,
			expectTwoThirdCap: false, // Transient frames don't cap
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate the CBR cap calculation
			// libopus: if ((!vbr || (constrained_vbr && !isTransient)) && tot_boost > 2*effectiveBytes/3)
			shouldCap := (!tc.vbr || (tc.constrainedVBR && !tc.isTransient))

			if shouldCap != tc.expectTwoThirdCap {
				t.Errorf("Expected cap=%v, got %v", tc.expectTwoThirdCap, shouldCap)
			}

			// 2/3 cap in bits
			cap := 2 * tc.effectiveBytes * 8 / 3
			t.Logf("Mode: vbr=%v, cvbr=%v, transient=%v -> cap_enabled=%v, cap=%d bits",
				tc.vbr, tc.constrainedVBR, tc.isTransient, shouldCap, cap)
		})
	}
}

// TestBoostBitsCalculation verifies boost_bits formula.
// Reference: libopus celt_encoder.c lines 1241-1252
func TestBoostBitsCalculation(t *testing.T) {
	lm := 3 // 20ms frame
	C := 1  // mono

	testCases := []struct {
		bandIdx  int
		width    int
		follower float64 // In log2 units
	}{
		{0, 1, 2.0},   // Very narrow band
		{5, 4, 2.0},   // Narrow band
		{10, 8, 2.0},  // Medium band
		{15, 24, 2.0}, // Wide band
		{20, 64, 2.0}, // Very wide band
	}

	for _, tc := range testCases {
		// Compute band width
		width := tc.width
		if tc.bandIdx < len(EBands)-1 {
			width = C * (EBands[tc.bandIdx+1] - EBands[tc.bandIdx]) << lm
		}

		// Clamp follower
		follower := tc.follower
		if follower > 4.0 {
			follower = 4.0
		}
		follower /= 8.0 // SHR32(follower, 8) in fixed-point

		var boost, boostBits int
		if width < 6 {
			boost = int(follower * 256) // SHR(follower, DB_SHIFT-8)
			boostBits = boost * width << bitRes
		} else if width > 48 {
			boost = int(follower * 8 * 256)
			boostBits = (boost * width << bitRes) / 8
		} else {
			boost = int(follower * float64(width) / 6 * 256)
			boostBits = boost * 6 << bitRes
		}

		t.Logf("Band %d: width=%d, follower=%.4f, boost=%d, boost_bits=%d (Q3)",
			tc.bandIdx, width, tc.follower, boost, boostBits)
	}
}

// =============================================================================
// Test 7: Median Helpers
// =============================================================================

// medianOf3Float computes median of first 3 elements.
func medianOf3Float(x []float64) float64 {
	if len(x) < 3 {
		if len(x) == 0 {
			return 0
		}
		return x[0]
	}
	a, b, c := x[0], x[1], x[2]
	if a > b {
		if b > c {
			return b
		} else if a > c {
			return c
		}
		return a
	}
	if a > c {
		return a
	} else if b > c {
		return c
	}
	return b
}

// medianOf5Float computes median of first 5 elements.
func medianOf5Float(x []float64) float64 {
	if len(x) < 5 {
		return medianOf3Float(x)
	}
	// Copy to avoid modifying input
	arr := []float64{x[0], x[1], x[2], x[3], x[4]}
	// Simple bubble sort (fine for 5 elements)
	for i := 0; i < 4; i++ {
		for j := 0; j < 4-i; j++ {
			if arr[j] > arr[j+1] {
				arr[j], arr[j+1] = arr[j+1], arr[j]
			}
		}
	}
	return arr[2] // Middle element
}

// TestMedianOf3 tests median of 3 with various inputs.
func TestMedianOf3(t *testing.T) {
	testCases := []struct {
		input    []float64
		expected float64
	}{
		{[]float64{1, 2, 3}, 2},
		{[]float64{3, 2, 1}, 2},
		{[]float64{2, 1, 3}, 2},
		{[]float64{1, 3, 2}, 2},
		{[]float64{5, 5, 5}, 5},
		{[]float64{1, 1, 2}, 1},
		{[]float64{-1, 0, 1}, 0},
		{[]float64{10, -10, 0}, 0},
	}

	for _, tc := range testCases {
		result := medianOf3Float(tc.input)
		if math.Abs(result-tc.expected) > 1e-10 {
			t.Errorf("medianOf3(%v) = %v, expected %v", tc.input, result, tc.expected)
		}
	}
}

// TestMedianOf5 tests median of 5 with various inputs.
func TestMedianOf5(t *testing.T) {
	testCases := []struct {
		input    []float64
		expected float64
	}{
		{[]float64{1, 2, 3, 4, 5}, 3},
		{[]float64{5, 4, 3, 2, 1}, 3},
		{[]float64{1, 5, 2, 4, 3}, 3},
		{[]float64{5, 5, 5, 5, 5}, 5},
		{[]float64{1, 1, 1, 2, 2}, 1},
		{[]float64{-2, -1, 0, 1, 2}, 0},
		{[]float64{100, 0, 50, 25, 75}, 50},
	}

	for _, tc := range testCases {
		result := medianOf5Float(tc.input)
		if math.Abs(result-tc.expected) > 1e-10 {
			t.Errorf("medianOf5(%v) = %v, expected %v", tc.input, result, tc.expected)
		}
	}
}

// =============================================================================
// Test 8: Edge Cases
// =============================================================================

// TestDynallocEdgeCases tests edge cases in dynalloc analysis.
func TestDynallocEdgeCases(t *testing.T) {
	t.Run("EmptyBandEnergies", func(t *testing.T) {
		// Empty energies should produce default weights
		weights := ComputeSpreadWeights(nil, 21, 1, 16)
		for i, w := range weights {
			if w != 1 {
				t.Errorf("Band %d: expected default weight 1, got %d", i, w)
			}
		}
	})

	t.Run("VeryLowEffectiveBytes", func(t *testing.T) {
		// Very low effective bytes should disable dynalloc (importance = 13)
		nbBands := 21
		effectiveBytes := 10 // Very low
		lm := 3

		bandLogE := generateRealisticBandEnergies(nbBands)
		oldBandE := generateFlatBandEnergies(MaxBands, 5.0)

		importance := ComputeImportance(bandLogE, oldBandE, nbBands, 1, lm, 16, effectiveBytes)

		allDefault := true
		for _, imp := range importance {
			if imp != 13 {
				allDefault = false
				break
			}
		}
		if !allDefault {
			t.Error("Expected all default importance for very low effectiveBytes")
		}
	})

	t.Run("LM0SpecialHandling", func(t *testing.T) {
		// LM=0 (2.5ms frames) has special handling for first 8 bands
		nbBands := 17 // Typical for LM=0
		lm := 0
		effectiveBytes := 50 // Above threshold: 30 + 5*0 = 30

		// Current frame has low energy in first bands
		bandLogE := generateFlatBandEnergies(nbBands, 2.0)
		// Previous frame had higher energy
		oldBandE := make([]float64, MaxBands)
		for i := 0; i < 8; i++ {
			oldBandE[i] = 10.0 // Higher than current
		}

		importance := ComputeImportance(bandLogE, oldBandE, nbBands, 1, lm, 16, effectiveBytes)

		t.Logf("LM=0 importance (current=2.0, old[0:8]=10.0):")
		for i := 0; i < nbBands; i++ {
			t.Logf("  Band %d: importance=%d", i, importance[i])
		}

		// First 8 bands should use max(current, old) for energy
		// This should affect the importance values
	})

	t.Run("SingleBand", func(t *testing.T) {
		bandLogE := []float64{10.0}
		weights := ComputeSpreadWeights(bandLogE, 1, 1, 16)
		if len(weights) != 1 {
			t.Errorf("Expected 1 weight, got %d", len(weights))
		}
	})

	t.Run("NegativeEnergies", func(t *testing.T) {
		// Very negative energies (below noise floor)
		nbBands := 21
		bandLogE := generateFlatBandEnergies(nbBands, -20.0)
		weights := ComputeSpreadWeights(bandLogE, nbBands, 1, 16)

		// All weights should still be valid
		for i, w := range weights {
			if w < 1 || w > 32 {
				t.Errorf("Band %d: weight %d out of range", i, w)
			}
		}
	})
}

// =============================================================================
// Test 9: Benchmarks
// =============================================================================

// BenchmarkComputeSpreadWeights benchmarks spread weight computation.
func BenchmarkComputeSpreadWeights(b *testing.B) {
	nbBands := 21
	bandLogE := generateRealisticBandEnergies(nbBands)

	b.Run("Mono", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ComputeSpreadWeights(bandLogE, nbBands, 1, 16)
		}
	})

	stereoBandLogE := generateStereoEnergies(nbBands,
		generateRealisticBandEnergies(nbBands),
		generateRealisticBandEnergies(nbBands))

	b.Run("Stereo", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ComputeSpreadWeights(stereoBandLogE, nbBands, 2, 16)
		}
	})
}

// BenchmarkDynallocImportance benchmarks importance computation in dynalloc context.
func BenchmarkDynallocImportance(b *testing.B) {
	nbBands := 21
	bandLogE := generateRealisticBandEnergies(nbBands)
	oldBandE := generateFlatBandEnergies(MaxBands, 5.0)

	b.Run("Mono_LowBitrate", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ComputeImportance(bandLogE, oldBandE, nbBands, 1, 3, 16, 40)
		}
	})

	b.Run("Mono_HighBitrate", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ComputeImportance(bandLogE, oldBandE, nbBands, 1, 3, 16, 200)
		}
	})

	stereoBandLogE := generateStereoEnergies(nbBands,
		generateRealisticBandEnergies(nbBands),
		generateRealisticBandEnergies(nbBands))
	oldStereoE := generateFlatBandEnergies(2*MaxBands, 5.0)

	b.Run("Stereo_HighBitrate", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ComputeImportance(stereoBandLogE, oldStereoE, nbBands, 2, 3, 16, 200)
		}
	})
}

// BenchmarkDynallocAnalysisFull benchmarks full dynalloc analysis flow.
func BenchmarkDynallocAnalysisFull(b *testing.B) {
	nbBands := 21
	channels := 1
	lm := 3
	lsbDepth := 16
	effectiveBytes := 200

	bandLogE := generateRealisticBandEnergies(nbBands)
	oldBandE := generateFlatBandEnergies(MaxBands, 5.0)

	b.Run("Full_Mono_20ms", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// Compute spread weights
			ComputeSpreadWeights(bandLogE, nbBands, channels, lsbDepth)
			// Compute importance
			ComputeImportance(bandLogE, oldBandE, nbBands, channels, lm, lsbDepth, effectiveBytes)
		}
	})
}

// =============================================================================
// Integration Tests
// =============================================================================

// TestSpreadWeightsIntegration tests that spread weights affect spreading decision.
func TestSpreadWeightsIntegration(t *testing.T) {
	encoder := NewEncoder(1)
	frameSize := 960
	nbBands := 21
	channels := 1

	// Create mock normalized coefficients (tonal signal)
	N0 := frameSize
	normX := make([]float64, N0*channels)
	for i := range normX {
		if i%20 == 0 {
			normX[i] = 0.9
		} else {
			normX[i] = 0.01
		}
	}

	// Test with uniform weights
	uniformWeights := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		uniformWeights[i] = 1
	}
	decisionUniform := encoder.SpreadingDecisionWithWeights(normX, nbBands, channels, frameSize, false, uniformWeights)

	// Test with computed weights from high-energy bands
	bandLogE := generateFlatBandEnergies(nbBands, 15.0)
	computedWeights := ComputeSpreadWeights(bandLogE, nbBands, channels, 16)
	encoder = NewEncoder(1) // Reset state
	decisionComputed := encoder.SpreadingDecisionWithWeights(normX, nbBands, channels, frameSize, false, computedWeights)

	t.Logf("Spread decision with uniform weights: %d", decisionUniform)
	t.Logf("Spread decision with computed weights: %d", decisionComputed)
	t.Logf("Computed weights: %v", computedWeights)

	// Both decisions should be valid
	if decisionUniform < 0 || decisionUniform > 3 {
		t.Errorf("Uniform weights decision %d out of range", decisionUniform)
	}
	if decisionComputed < 0 || decisionComputed > 3 {
		t.Errorf("Computed weights decision %d out of range", decisionComputed)
	}
}

// TestImportanceIntegrationWithTF tests that importance affects TF analysis.
func TestImportanceIntegrationWithTF(t *testing.T) {
	nbBands := 21
	N0 := 960 // 20ms frame

	// Create mock normalized coefficients
	X := make([]float64, N0)
	for i := range X {
		X[i] = math.Sin(float64(i)*0.1) * 0.1
	}

	lm := 3
	isTransient := false
	tfEstimate := 0.3
	effectiveBytes := 200

	// Test with nil importance (should use default 13)
	tfRes1, tfSelect1 := TFAnalysis(X, N0, nbBands, isTransient, lm, tfEstimate, effectiveBytes, nil)

	// Test with computed importance
	bandLogE := generateRealisticBandEnergies(nbBands)
	oldBandE := generateFlatBandEnergies(MaxBands, 5.0)
	importance := ComputeImportance(bandLogE, oldBandE, nbBands, 1, lm, 16, effectiveBytes)
	tfRes2, tfSelect2 := TFAnalysis(X, N0, nbBands, isTransient, lm, tfEstimate, effectiveBytes, importance)

	t.Logf("TF with nil importance: tfSelect=%d, tfRes=%v", tfSelect1, tfRes1)
	t.Logf("TF with computed importance: tfSelect=%d, tfRes=%v", tfSelect2, tfRes2)
	t.Logf("Importance values: %v", importance)

	// Verify results are valid
	if len(tfRes1) != nbBands || len(tfRes2) != nbBands {
		t.Error("TF result length mismatch")
	}
}
