package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestIMDCTCompare(t *testing.T) {
	// Create test spectrum
	spectrum := make([]float64, 480)
	for i := range spectrum {
		spectrum[i] = math.Sin(float64(i) * 0.1)
	}

	// Test 1: Direct IMDCT (reference)
	direct := IMDCTDirect(spectrum)
	fmt.Printf("IMDCTDirect output: len=%d\n", len(direct))
	fmt.Printf("  First 10: ")
	for i := 0; i < 10 && i < len(direct); i++ {
		fmt.Printf("%.4f ", direct[i])
	}
	fmt.Println()
	fmt.Printf("  Last 10 (at %d): ", len(direct)-10)
	for i := len(direct) - 10; i < len(direct); i++ {
		fmt.Printf("%.4f ", direct[i])
	}
	fmt.Println()

	// Test 2: FFT-based IMDCT via IMDCTOverlap
	overlap := 120
	prev := make([]float64, overlap)
	ffted := IMDCTOverlapWithPrev(spectrum, prev, overlap)
	fmt.Printf("\nIMDCTOverlapWithPrev output: len=%d (expected=%d)\n", len(ffted), len(spectrum)+overlap)
	fmt.Printf("  First 10: ")
	for i := 0; i < 10 && i < len(ffted); i++ {
		fmt.Printf("%.4f ", ffted[i])
	}
	fmt.Println()
	fmt.Printf("  Last 10 (at %d): ", len(ffted)-10)
	for i := len(ffted) - 10; i < len(ffted); i++ {
		fmt.Printf("%.4f ", ffted[i])
	}
	fmt.Println()

	// Check the "new overlap" region
	newOverlap := ffted[480:]
	fmt.Printf("\nNew overlap region [480:600]:\n")
	fmt.Printf("  First 10: ")
	for i := 0; i < 10 && i < len(newOverlap); i++ {
		fmt.Printf("%.4f ", newOverlap[i])
	}
	fmt.Println()
	fmt.Printf("  Middle 10 (at 55): ")
	for i := 55; i < 65 && i < len(newOverlap); i++ {
		fmt.Printf("%.4f ", newOverlap[i])
	}
	fmt.Println()
	fmt.Printf("  Last 10 (at %d): ", len(newOverlap)-10)
	for i := len(newOverlap) - 10; i < len(newOverlap); i++ {
		fmt.Printf("%.4f ", newOverlap[i])
	}
	fmt.Println()

	// Count zeros in new overlap
	zeroCount := 0
	for _, v := range newOverlap {
		if v == 0 {
			zeroCount++
		}
	}
	fmt.Printf("  Zeros in new overlap: %d out of %d\n", zeroCount, len(newOverlap))

	// Compare with what direct IMDCT produces at end
	fmt.Printf("\nComparison with direct IMDCT tail (should be in new overlap):\n")
	fmt.Printf("  Direct IMDCT [900:910]: ")
	for i := 900; i < 910; i++ {
		fmt.Printf("%.4f ", direct[i])
	}
	fmt.Println()
	fmt.Printf("  Direct IMDCT [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.4f ", direct[i])
	}
	fmt.Println()

	// The key insight: IMDCTDirect produces 960 samples
	// The second half (480:960) should become the overlap for next frame
	// But IMDCTOverlapWithPrev only produces 600 samples
	// So it's missing 360 samples!
	fmt.Printf("\nExpected full IMDCT size: %d\n", 2*len(spectrum))
	fmt.Printf("Actual IMDCTOverlapWithPrev size: %d\n", len(ffted))
	fmt.Printf("Missing samples: %d\n", 2*len(spectrum)-len(ffted))
}
