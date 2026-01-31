package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestIMDCTOverlapDetail(t *testing.T) {
	N := 480
	overlap := 120
	spectrum := make([]float64, N)
	for i := 0; i < N; i++ {
		spectrum[i] = math.Sin(float64(i)*0.1) * 0.1
	}

	// Get full IMDCT before any windowing
	imdctFull := IMDCTDirect(spectrum)
	n2 := len(imdctFull)

	fmt.Printf("Full IMDCT: %d samples\n", n2)

	// Compute energy by region
	var e0, e1, e2, e3 float64
	for i := 0; i < overlap; i++ {
		e0 += imdctFull[i] * imdctFull[i]
	}
	for i := overlap; i < N; i++ {
		e1 += imdctFull[i] * imdctFull[i]
	}
	for i := N; i < n2-overlap; i++ {
		e2 += imdctFull[i] * imdctFull[i]
	}
	for i := n2 - overlap; i < n2; i++ {
		e3 += imdctFull[i] * imdctFull[i]
	}

	fmt.Printf("Energy by region:\n")
	fmt.Printf("  [0:%d] (windowed start): %.6f\n", overlap, e0)
	fmt.Printf("  [%d:%d] (unwindowed middle-1): %.6f\n", overlap, N, e1)
	fmt.Printf("  [%d:%d] (unwindowed middle-2): %.6f\n", N, n2-overlap, e2)
	fmt.Printf("  [%d:%d] (windowed end): %.6f\n", n2-overlap, n2, e3)
	fmt.Printf("  Total: %.6f\n", e0+e1+e2+e3)

	// Now test imdctOverlapWithPrev
	prevOverlap := make([]float64, overlap)
	result := imdctOverlapWithPrev(spectrum, prevOverlap, overlap)

	fmt.Printf("\nimdctOverlapWithPrev result: %d samples\n", len(result))

	// Check energy in result
	var re0, re1, re2 float64
	for i := 0; i < overlap && i < len(result); i++ {
		re0 += result[i] * result[i]
	}
	for i := overlap; i < N && i < len(result); i++ {
		re1 += result[i] * result[i]
	}
	for i := N; i < len(result); i++ {
		re2 += result[i] * result[i]
	}

	fmt.Printf("Result energy by region:\n")
	fmt.Printf("  [0:%d] (overlap-add): %.6f\n", overlap, re0)
	fmt.Printf("  [%d:%d] (middle): %.6f\n", overlap, N, re1)
	fmt.Printf("  [%d:%d] (new overlap): %.6f\n", N, len(result), re2)

	// Compare specific samples
	fmt.Printf("\nSample comparison (middle region, should be unchanged):\n")
	for i := overlap; i < overlap+10 && i < N; i++ {
		fmt.Printf("  [%d]: result=%.6f, full=%.6f\n", i, result[i], imdctFull[i])
	}

	// Check windowed start region
	window := GetWindowBuffer(overlap)
	fmt.Printf("\nWindowed start region:\n")
	for i := 0; i < 10; i++ {
		windowed := imdctFull[i] * window[i]
		fmt.Printf("  [%d]: result=%.6f, full=%.6f, windowed=%.6f, window=%.6f\n",
			i, result[i], imdctFull[i], windowed, window[i])
	}
}
