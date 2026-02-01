package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestIMDCTSingleFrequency(t *testing.T) {
	n2 := 960

	// Test with coefficient at frequency 100 instead of DC
	spectrum := make([]float64, n2)
	spectrum[100] = 1.0

	overlap := 120
	prevOverlap := make([]float64, overlap)

	// Run DFT-based IMDCT
	result := imdctOverlapWithPrev(spectrum, prevOverlap, overlap)

	fmt.Println("=== IMDCT of single freq at bin 100 ===")
	fmt.Printf("First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.4f ", result[i])
	}
	fmt.Printf("\n[950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.4f ", result[i])
	}
	fmt.Println()

	// Compare with Direct IMDCT
	directOut := IMDCTDirect(spectrum)
	fmt.Printf("\nDirect IMDCT first 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.4f ", directOut[i])
	}
	fmt.Printf("\nDirect IMDCT [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.4f ", directOut[i])
	}
	fmt.Println()

	// Check if DFT-based also has linear growth for non-DC
	var maxMagnitude float64
	maxIdx := 0
	for i, v := range result[:n2] {
		if math.Abs(v) > maxMagnitude {
			maxMagnitude = math.Abs(v)
			maxIdx = i
		}
	}
	fmt.Printf("\nDFT-based max magnitude: %.4f at index %d\n", maxMagnitude, maxIdx)

	var directMax float64
	directMaxIdx := 0
	for i, v := range directOut[:n2] {
		if math.Abs(v) > directMax {
			directMax = math.Abs(v)
			directMaxIdx = i
		}
	}
	fmt.Printf("Direct max magnitude: %.4f at index %d\n", directMax, directMaxIdx)
}

func TestIMDCTRealishSpectrum(t *testing.T) {
	// Test with a spectrum that resembles real audio (energy spread across bins)
	n2 := 960

	spectrum := make([]float64, n2)
	// Simulate typical audio: more energy in low frequencies, less in high
	for i := 0; i < n2; i++ {
		// Exponential decay with some variation
		spectrum[i] = math.Sin(float64(i)*0.3) * math.Exp(-float64(i)/200.0)
	}

	overlap := 120
	prevOverlap := make([]float64, overlap)

	// Run DFT-based IMDCT
	result := imdctOverlapWithPrev(spectrum, prevOverlap, overlap)

	fmt.Println("\n=== IMDCT of realistic spectrum ===")
	fmt.Printf("First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.4f ", result[i])
	}
	fmt.Printf("\n[950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.4f ", result[i])
	}
	fmt.Println()

	// Check for linear growth (compare first and last magnitudes)
	firstAvg := 0.0
	lastAvg := 0.0
	for i := 0; i < 10; i++ {
		firstAvg += math.Abs(result[i])
		lastAvg += math.Abs(result[n2-10+i])
	}
	firstAvg /= 10
	lastAvg /= 10

	fmt.Printf("\nAvg magnitude first 10: %.4f, last 10: %.4f, ratio: %.2f\n",
		firstAvg, lastAvg, lastAvg/firstAvg)

	// Compare with Direct
	directOut := IMDCTDirect(spectrum)
	directFirstAvg := 0.0
	directLastAvg := 0.0
	for i := 0; i < 10; i++ {
		directFirstAvg += math.Abs(directOut[i])
		directLastAvg += math.Abs(directOut[n2-10+i])
	}
	directFirstAvg /= 10
	directLastAvg /= 10

	fmt.Printf("Direct avg magnitude first 10: %.4f, last 10: %.4f, ratio: %.2f\n",
		directFirstAvg, directLastAvg, directLastAvg/directFirstAvg)
}
