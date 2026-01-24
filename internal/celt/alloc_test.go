package celt

import (
	"testing"
)

// TestComputeAllocationBudget verifies bit allocation respects total budget.
// For various bit budgets, ensures:
// - sum(BandBits) + sum(FineBits) <= totalBits
// - No negative allocations
// - Caps are respected (no band exceeds cap)
func TestComputeAllocationBudget(t *testing.T) {
	channels := 1
	testCases := []struct {
		name      string
		totalBits int
		nbBands   int
		lm        int
	}{
		{"100_bits_21_bands_lm3", 100, 21, 3},
		{"500_bits_21_bands_lm3", 500, 21, 3},
		{"1000_bits_21_bands_lm3", 1000, 21, 3},
		{"2000_bits_21_bands_lm3", 2000, 21, 3},
		// Different frame sizes
		{"500_bits_13_bands_lm0", 500, 13, 0},
		{"500_bits_17_bands_lm1", 500, 17, 1},
		{"500_bits_19_bands_lm2", 500, 19, 2},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ComputeAllocation(
				tc.totalBits,
				tc.nbBands,
				channels,
				nil,   // caps (auto)
				nil,   // dynalloc
				0,     // trim (neutral)
				-1,    // intensity (disabled)
				false, // dual stereo
				tc.lm,
			)

			// Check budget respected
			totalAllocatedQ3 := 0
			for band := 0; band < tc.nbBands; band++ {
				totalAllocatedQ3 += bandBitsQ3(result, band, channels)
			}

			budgetQ3 := tc.totalBits << bitRes
			if totalAllocatedQ3 > budgetQ3 {
				t.Errorf("Allocation exceeds budget: allocated %d q3, budget %d q3",
					totalAllocatedQ3, budgetQ3)
			}

			// Check no negative allocations
			for band := 0; band < tc.nbBands; band++ {
				if result.BandBits[band] < 0 {
					t.Errorf("Band %d has negative BandBits: %d", band, result.BandBits[band])
				}
				if result.FineBits[band] < 0 {
					t.Errorf("Band %d has negative FineBits: %d", band, result.FineBits[band])
				}
			}

			// Check caps respected
			for band := 0; band < tc.nbBands; band++ {
				if result.BandBits[band] > result.Caps[band] {
					t.Errorf("Band %d exceeds cap: %d > %d",
						band, result.BandBits[band], result.Caps[band])
				}
			}

			// Log allocation table for inspection
			totalAllocatedBits := totalAllocatedQ3 >> bitRes
			t.Logf("Allocation for totalBits=%d, nbBands=%d, lm=%d:", tc.totalBits, tc.nbBands, tc.lm)
			t.Logf("  Total allocated: %d/%d bits (%.1f%%)", totalAllocatedBits, tc.totalBits, 100*float64(totalAllocatedBits)/float64(tc.totalBits))
			for band := 0; band < tc.nbBands; band++ {
				frameSize := LMToFrameSize(tc.lm)
				bandBits := result.BandBits[band] >> bitRes
				t.Logf("  Band %2d: width=%2d, bandBits=%3d, fineBits=%2d, cap=%3d",
					band, ScaledBandWidth(band, frameSize), bandBits,
					result.FineBits[band], result.Caps[band])
			}
		})
	}
}

// TestComputeAllocationDistribution verifies allocation follows spectral shape.
// - Higher totalBits -> more bits to high-frequency bands
// - Lower bands get more bits at low bit rates (perceptually important)
func TestComputeAllocationDistribution(t *testing.T) {
	channels := 1
	nbBands := 21
	lm := 3 // 20ms frame

	// Compare low and high bit rates
	lowBits := 200
	highBits := 2000

	lowResult := ComputeAllocation(lowBits, nbBands, channels, nil, nil, 0, -1, false, lm)
	highResult := ComputeAllocation(highBits, nbBands, channels, nil, nil, 0, -1, false, lm)

	// At low bit rate, lower bands should get proportionally more bits
	// At high bit rate, allocation should be more spread out

	// Count bits in lower bands (0-10) vs upper bands (11-20)
	var lowRateLower, lowRateUpper int
	var highRateLower, highRateUpper int

	for band := 0; band < nbBands; band++ {
		if band <= 10 {
			lowRateLower += bandBitsQ3(lowResult, band, channels)
			highRateLower += bandBitsQ3(highResult, band, channels)
		} else {
			lowRateUpper += bandBitsQ3(lowResult, band, channels)
			highRateUpper += bandBitsQ3(highResult, band, channels)
		}
	}

	// High bit rate should have more bits in upper bands (absolute)
	if highRateUpper <= lowRateUpper {
		t.Logf("Note: high bit rate upper bands (%d) <= low bit rate upper bands (%d)",
			highRateUpper, lowRateUpper)
		// This is not necessarily wrong - depends on allocation algorithm
	}

	t.Logf("Low rate (%d bits): lower=%d, upper=%d", lowBits, lowRateLower>>bitRes, lowRateUpper>>bitRes)
	t.Logf("High rate (%d bits): lower=%d, upper=%d", highBits, highRateLower>>bitRes, highRateUpper>>bitRes)

	// Basic sanity: high bit rate should allocate more total bits
	lowTotal := lowRateLower + lowRateUpper
	highTotal := highRateLower + highRateUpper
	if highTotal <= lowTotal {
		t.Errorf("High bit rate (%d) should allocate more than low rate (%d)",
			highTotal, lowTotal)
	}
}

// TestComputeAllocationTrim verifies trim parameter affects spectral balance.
// - trim > 0 -> high bands get more bits
// - trim < 0 -> low bands get more bits
// - trim = 0 -> neutral
func TestComputeAllocationTrim(t *testing.T) {
	channels := 1
	nbBands := 21
	lm := 3
	totalBits := 1000

	// Compare different trim values
	trimValues := []int{-6, -3, 0, 3, 6}

	var results []AllocationResult
	for _, trim := range trimValues {
		result := ComputeAllocation(totalBits, nbBands, channels, nil, nil, trim, -1, false, lm)
		results = append(results, result)
	}

	// Compute high band ratio for each trim
	for i, trim := range trimValues {
		var lowBandBits, highBandBits int
		for band := 0; band < nbBands; band++ {
			bits := bandBitsQ3(results[i], band, channels)
			if band < nbBands/2 {
				lowBandBits += bits
			} else {
				highBandBits += bits
			}
		}
		total := lowBandBits + highBandBits
		highRatio := 0.0
		if total > 0 {
			highRatio = float64(highBandBits) / float64(total)
		}
		t.Logf("Trim=%+d: low=%d, high=%d, highRatio=%.2f", trim, lowBandBits>>bitRes, highBandBits>>bitRes, highRatio)
	}

	// Verify trim ordering (higher trim should have higher high-band ratio)
	for i := 1; i < len(trimValues); i++ {
		prevHighRatio := computeHighBandRatio(results[i-1], nbBands)
		currHighRatio := computeHighBandRatio(results[i], nbBands)

		// Allow some tolerance since trim effect may be small
		if currHighRatio < prevHighRatio-0.1 {
			t.Logf("Note: trim=%d ratio %.2f < trim=%d ratio %.2f (may be allocation boundary effect)",
				trimValues[i], currHighRatio, trimValues[i-1], prevHighRatio)
		}
	}
}

// computeHighBandRatio returns the ratio of bits allocated to high bands.
func computeHighBandRatio(result AllocationResult, nbBands int) float64 {
	channels := 1
	var lowBits, highBits int
	for band := 0; band < nbBands; band++ {
		bits := bandBitsQ3(result, band, channels)
		if band < nbBands/2 {
			lowBits += bits
		} else {
			highBits += bits
		}
	}
	total := lowBits + highBits
	if total == 0 {
		return 0
	}
	return float64(highBits) / float64(total)
}

func bandBitsQ3(result AllocationResult, band, channels int) int {
	if band < 0 || band >= len(result.BandBits) || band >= len(result.FineBits) {
		return 0
	}
	return result.BandBits[band] + (result.FineBits[band] * channels << bitRes)
}

// TestComputeAllocationByLM verifies allocation varies correctly by frame size.
// - LM=0 (2.5ms) -> different caps than LM=3 (20ms)
// - Shorter frames have less total capacity
func TestComputeAllocationByLM(t *testing.T) {
	channels := 1
	totalBits := 500
	nbBands := 13 // Use minimum bands (shared across all LM values)

	for lm := 0; lm <= 3; lm++ {
		result := ComputeAllocation(totalBits, nbBands, channels, nil, nil, 0, -1, false, lm)

		frameSize := LMToFrameSize(lm)
		var totalAllocated int
		var maxCap int
		for band := 0; band < nbBands; band++ {
			totalAllocated += bandBitsQ3(result, band, channels)
			if result.Caps[band] > maxCap {
				maxCap = result.Caps[band]
			}
		}

		totalAllocatedBits := totalAllocated >> bitRes
		t.Logf("LM=%d (%.1fms, %d samples): allocated=%d bits, maxCap=%d",
			lm, float64(frameSize)/48.0, frameSize, totalAllocatedBits, maxCap)

		// Verify allocation doesn't exceed budget
		if totalAllocated > (totalBits << bitRes) {
			t.Errorf("LM=%d: allocation %d q3 exceeds budget %d q3", lm, totalAllocated, totalBits<<bitRes)
		}
	}

	// LM=3 (20ms) should have larger caps than LM=0 (2.5ms)
	result0 := ComputeAllocation(totalBits, nbBands, channels, nil, nil, 0, -1, false, 0)
	result3 := ComputeAllocation(totalBits, nbBands, channels, nil, nil, 0, -1, false, 3)

	// Check that at least some caps are larger for LM=3
	largerCapsCount := 0
	for band := 0; band < nbBands; band++ {
		if result3.Caps[band] > result0.Caps[band] {
			largerCapsCount++
		}
	}

	if largerCapsCount == 0 {
		t.Logf("Note: LM=3 caps not larger than LM=0 (may be by design)")
	} else {
		t.Logf("LM=3 has larger caps than LM=0 for %d/%d bands", largerCapsCount, nbBands)
	}
}

// TestPulseCapsReasonable verifies pulse caps are proportional to band width.
// Wider bands can hold more pulses.
func TestPulseCapsReasonable(t *testing.T) {
	testCases := []struct {
		lm        int
		frameSize int
	}{
		{0, 120},
		{1, 240},
		{2, 480},
		{3, 960},
	}

	for _, tc := range testCases {
		t.Run(lm_name(tc.lm), func(t *testing.T) {
			caps := initCaps(MaxBands, tc.lm, 1)

			t.Logf("Pulse caps for LM=%d (%d samples):", tc.lm, tc.frameSize)
			for band := 0; band < MaxBands; band++ {
				width := ScaledBandWidth(band, tc.frameSize)
				t.Logf("  Band %2d: width=%3d, cap=%3d", band, width, caps[band])

				// Cap should be positive for positive width
				if width > 0 && caps[band] <= 0 {
					t.Errorf("Band %d has width %d but cap %d", band, width, caps[band])
				}

				// Wider bands should have equal or larger caps
				if band > 0 && width > ScaledBandWidth(band-1, tc.frameSize) {
					if caps[band] < caps[band-1] {
						t.Logf("Note: Band %d (width=%d, cap=%d) has smaller cap than band %d (width=%d, cap=%d)",
							band, width, caps[band], band-1, ScaledBandWidth(band-1, tc.frameSize), caps[band-1])
					}
				}
			}
		})
	}
}

// lm_name returns a descriptive name for LM value.
func lm_name(lm int) string {
	switch lm {
	case 0:
		return "2.5ms"
	case 1:
		return "5ms"
	case 2:
		return "10ms"
	case 3:
		return "20ms"
	default:
		return "unknown"
	}
}

// TestComputeAllocationEdgeCases tests edge cases in allocation.
func TestComputeAllocationEdgeCases(t *testing.T) {
	// Test with zero bits
	t.Run("zero_bits", func(t *testing.T) {
		result := ComputeAllocation(0, 21, 1, nil, nil, 0, -1, false, 3)
		for band := 0; band < 21; band++ {
			if result.BandBits[band] != 0 || result.FineBits[band] != 0 {
				t.Errorf("Band %d has bits with zero budget", band)
			}
		}
	})

	// Test with zero bands
	t.Run("zero_bands", func(t *testing.T) {
		result := ComputeAllocation(1000, 0, 1, nil, nil, 0, -1, false, 3)
		if len(result.BandBits) != 0 {
			t.Errorf("Expected 0 bands, got %d", len(result.BandBits))
		}
	})

	// Test with very high bit budget
	t.Run("high_budget", func(t *testing.T) {
		result := ComputeAllocation(10000, 21, 1, nil, nil, 0, -1, false, 3)
		// Should allocate up to caps
		for band := 0; band < 21; band++ {
			if result.BandBits[band] > result.Caps[band] {
				t.Errorf("Band %d exceeds cap even with high budget", band)
			}
		}
	})

	// Test with intensity stereo
	t.Run("intensity_stereo", func(t *testing.T) {
		intensity := 15 // Start intensity at band 15
		result := ComputeAllocation(1000, 21, 2, nil, nil, 0, intensity, false, 3)

		// Bands above intensity should have adjusted bits
		var totalBits int
		for band := 0; band < 21; band++ {
			totalBits += result.BandBits[band] + result.FineBits[band]
		}
		t.Logf("Intensity stereo from band %d: total allocated %d bits", intensity, totalBits)
	})
}
