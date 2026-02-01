package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestShortIMDCTDistribution(t *testing.T) {
	// Test IMDCT output distribution for short block (n=120)
	n := 120
	spectrum := make([]float64, n)
	for i := 0; i < n; i++ {
		spectrum[i] = math.Sin(float64(i)*0.1) * 0.1
	}

	// Get full IMDCT
	imdctFull := IMDCTDirect(spectrum)
	fmt.Printf("IMDCT of %d coefficients -> %d samples\n", n, len(imdctFull))

	// Compute energy in each half
	var e1, e2 float64
	for i := 0; i < n; i++ {
		e1 += imdctFull[i] * imdctFull[i]
	}
	for i := n; i < 2*n; i++ {
		e2 += imdctFull[i] * imdctFull[i]
	}

	fmt.Printf("Energy in first half [0:%d]: %.6f\n", n, e1)
	fmt.Printf("Energy in second half [%d:%d]: %.6f\n", n, 2*n, e2)
	fmt.Printf("Ratio (first/second): %.4f\n", e1/e2)

	// Check specific values
	fmt.Printf("\nFirst 10 samples:\n")
	for i := 0; i < 10; i++ {
		fmt.Printf("  [%d] = %.6f\n", i, imdctFull[i])
	}
	fmt.Printf("Last 10 samples of first half:\n")
	for i := n - 10; i < n; i++ {
		fmt.Printf("  [%d] = %.6f\n", i, imdctFull[i])
	}
	fmt.Printf("First 10 samples of second half:\n")
	for i := n; i < n+10; i++ {
		fmt.Printf("  [%d] = %.6f\n", i, imdctFull[i])
	}

	// Now test with prevOverlap = zeros
	overlap := 120
	prevOverlap := make([]float64, overlap)
	result := imdctOverlapWithPrev(spectrum, prevOverlap, overlap)

	fmt.Printf("\nimdctOverlapWithPrev result: %d samples\n", len(result))

	// Check energy in output and new overlap
	var eOut, eOvl float64
	for i := 0; i < n && i < len(result); i++ {
		eOut += result[i] * result[i]
	}
	for i := n; i < len(result); i++ {
		eOvl += result[i] * result[i]
	}

	fmt.Printf("Output energy [0:%d]: %.6f\n", n, eOut)
	fmt.Printf("New overlap energy [%d:%d]: %.6f\n", n, len(result), eOvl)

	// Check specific output values
	fmt.Printf("\nOutput first 10:\n")
	for i := 0; i < 10 && i < len(result); i++ {
		fmt.Printf("  [%d] = %.6f\n", i, result[i])
	}
}
