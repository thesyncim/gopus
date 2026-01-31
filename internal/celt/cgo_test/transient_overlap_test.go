// transient_overlap_test.go - Debug transient overlap handling
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// TestTransientOverlapFill checks what values are in the overlap buffer after transient synthesis
func TestTransientOverlapFill(t *testing.T) {
	// Simulate transient synthesis with known inputs
	frameSize := 960
	overlap := 120
	shortBlocks := 8
	shortSize := frameSize / shortBlocks // 120

	// Create test coefficients (simple pattern)
	coeffs := make([]float64, frameSize)
	for i := range coeffs {
		coeffs[i] = float64(i%100) / 100.0
	}

	// Simulate previous overlap (all ones for visibility)
	prevOverlap := make([]float64, overlap)
	for i := range prevOverlap {
		prevOverlap[i] = 1.0
	}

	// Call synthesis
	output, newOverlap := celt.SynthesizeWithConfig(coeffs, overlap, true, shortBlocks, prevOverlap)

	t.Logf("Output length: %d", len(output))
	t.Logf("New overlap length: %d", len(newOverlap))

	if len(output) != frameSize {
		t.Errorf("Expected output length %d, got %d", frameSize, len(output))
	}

	// Check new overlap for zeros
	zeroCount := 0
	nonZeroCount := 0
	for i, v := range newOverlap {
		if v == 0 {
			zeroCount++
			if i < 10 || i >= len(newOverlap)-10 {
				t.Logf("  newOverlap[%d] = 0 (ZERO)", i)
			}
		} else {
			nonZeroCount++
			if i < 5 || i >= len(newOverlap)-5 {
				t.Logf("  newOverlap[%d] = %.6f", i, v)
			}
		}
	}
	t.Logf("Zero count in new overlap: %d / %d", zeroCount, len(newOverlap))

	// Check the overlap region boundary
	if zeroCount > overlap/2 {
		t.Errorf("Too many zeros in new overlap (%d out of %d) - likely bug in transient synthesis", zeroCount, len(newOverlap))
	}

	// Test with shortSize boundary analysis
	t.Log("\nAnalyzing by short block boundaries:")
	for b := 0; b < shortBlocks; b++ {
		start := b * shortSize
		end := (b + 1) * shortSize
		t.Logf("  Block %d output range: [%d, %d)", b, start, end)

		// Check corresponding overlap positions
		overlapStart := start + shortSize // Position in extended buffer
		if overlapStart >= frameSize && overlapStart < frameSize+overlap {
			relPos := overlapStart - frameSize
			t.Logf("    Contributes to overlap[%d:]", relPos)
		}
	}
}
