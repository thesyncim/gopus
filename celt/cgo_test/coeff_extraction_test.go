//go:build cgo_libopus
// +build cgo_libopus

// coeff_extraction_test.go - Test coefficient extraction for short blocks
package cgo

import (
	"testing"
)

// TestShortBlockCoefficientExtraction verifies the interleaved coefficient extraction
func TestShortBlockCoefficientExtraction(t *testing.T) {
	frameSize := 960
	shortBlocks := 8
	shortSize := frameSize / shortBlocks // 120

	// Create test coefficients with known pattern
	// coeffs[i] = i so we can verify extraction
	coeffs := make([]float64, frameSize)
	for i := range coeffs {
		coeffs[i] = float64(i)
	}

	// Extract coefficients for each block and verify
	for b := 0; b < shortBlocks; b++ {
		shortCoeffs := make([]float64, shortSize)
		for i := 0; i < shortSize; i++ {
			idx := b + i*shortBlocks
			if idx < frameSize {
				shortCoeffs[i] = coeffs[idx]
			}
		}

		// Verify: shortCoeffs[i] should be coeffs[b + i*8]
		// For b=0: 0, 8, 16, ..., 952
		// For b=7: 7, 15, 23, ..., 959

		expectedFirst := float64(b)
		expectedSecond := float64(b + shortBlocks)
		expectedLast := float64(b + (shortSize-1)*shortBlocks)

		if shortCoeffs[0] != expectedFirst {
			t.Errorf("Block %d: shortCoeffs[0] = %v, expected %v", b, shortCoeffs[0], expectedFirst)
		}
		if shortCoeffs[1] != expectedSecond {
			t.Errorf("Block %d: shortCoeffs[1] = %v, expected %v", b, shortCoeffs[1], expectedSecond)
		}
		if shortCoeffs[shortSize-1] != expectedLast {
			t.Errorf("Block %d: shortCoeffs[%d] = %v, expected %v", b, shortSize-1, shortCoeffs[shortSize-1], expectedLast)
		}

		t.Logf("Block %d: first=%v, second=%v, last=%v (indices %d, %d, %d)",
			b, shortCoeffs[0], shortCoeffs[1], shortCoeffs[shortSize-1],
			b, b+shortBlocks, b+(shortSize-1)*shortBlocks)
	}

	// Verify total coverage - all coefficients should be used exactly once
	used := make([]bool, frameSize)
	for b := 0; b < shortBlocks; b++ {
		for i := 0; i < shortSize; i++ {
			idx := b + i*shortBlocks
			if used[idx] {
				t.Errorf("Coefficient %d used multiple times", idx)
			}
			used[idx] = true
		}
	}
	for i, u := range used {
		if !u {
			t.Errorf("Coefficient %d not used", i)
		}
	}
	t.Log("All coefficients used exactly once: PASS")
}

// TestLibopusShortBlockCoeffOrder shows what order libopus expects
// libopus uses &freq[b] with stride B, which reads freq[b], freq[b+2B], freq[b+4B], ...
func TestLibopusShortBlockCoeffOrder(t *testing.T) {
	t.Log("libopus clt_mdct_backward coefficient access pattern:")
	t.Log("With &freq[b] and stride=B=8:")
	t.Log("")

	B := 8       // Number of short blocks
	N2 := 120    // Short block size
	N4 := N2 / 2 // = 60

	t.Log("Pre-rotation loop (i = 0 to N4-1 = 59):")
	t.Log("  xp1 reads: freq[b], freq[b+2B], freq[b+4B], ... (via xp1 += 2*stride)")
	t.Log("  xp2 reads: freq[b+B*(N2-1)], freq[b+B*(N2-3)], ... (via xp2 -= 2*stride)")
	t.Log("")

	// Calculate actual indices for block 0
	b := 0
	t.Logf("Block 0 access pattern:")
	var xp1Indices, xp2Indices []int
	xp1 := b
	xp2 := b + B*(N2-1)
	for i := 0; i < N4; i++ {
		xp1Indices = append(xp1Indices, xp1)
		xp2Indices = append(xp2Indices, xp2)
		xp1 += 2 * B
		xp2 -= 2 * B
	}
	t.Logf("  xp1: %v ... %v (total %d values)", xp1Indices[:5], xp1Indices[len(xp1Indices)-5:], len(xp1Indices))
	t.Logf("  xp2: %v ... %v (total %d values)", xp2Indices[:5], xp2Indices[len(xp2Indices)-5:], len(xp2Indices))

	// Combined unique indices
	allIndices := make(map[int]bool)
	for _, idx := range xp1Indices {
		allIndices[idx] = true
	}
	for _, idx := range xp2Indices {
		allIndices[idx] = true
	}
	t.Logf("  Total unique indices: %d (should be %d)", len(allIndices), N2)

	// Compare with gopus extraction
	t.Log("")
	t.Log("gopus extraction pattern (b + i*shortBlocks for i in 0..119):")
	var gopusIndices []int
	for i := 0; i < N2; i++ {
		idx := b + i*B
		gopusIndices = append(gopusIndices, idx)
	}
	t.Logf("  gopus: %v ... %v (total %d values)", gopusIndices[:5], gopusIndices[len(gopusIndices)-5:], len(gopusIndices))

	// Check if they cover the same indices
	gopusSet := make(map[int]bool)
	for _, idx := range gopusIndices {
		gopusSet[idx] = true
	}

	match := true
	for idx := range allIndices {
		if !gopusSet[idx] {
			t.Logf("  Index %d in libopus but not in gopus", idx)
			match = false
		}
	}
	for idx := range gopusSet {
		if !allIndices[idx] {
			t.Logf("  Index %d in gopus but not in libopus", idx)
			match = false
		}
	}
	if match {
		t.Log("  Index sets MATCH")
	} else {
		t.Log("  Index sets DO NOT MATCH - this is the bug!")
	}
}
