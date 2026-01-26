package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestLibopusIMDCT_DCImpulse(t *testing.T) {
	// Test with DC impulse - this is where the old implementation fails
	n2 := 960
	spectrum := make([]float64, n2)
	spectrum[0] = 1.0

	overlap := 120
	prevOverlap := make([]float64, overlap)

	// Run libopus-style IMDCT
	result := libopusIMDCT(spectrum, prevOverlap, overlap)

	// Check for NaN/Inf
	hasNaN := false
	hasInf := false
	for i, v := range result {
		if math.IsNaN(v) {
			hasNaN = true
			t.Errorf("NaN at index %d", i)
		}
		if math.IsInf(v, 0) {
			hasInf = true
			t.Errorf("Inf at index %d", i)
		}
	}

	if hasNaN || hasInf {
		t.Fatal("Result contains NaN or Inf values")
	}

	fmt.Printf("libopusIMDCT DC impulse test:\n")
	fmt.Printf("  Output length: %d (expected %d)\n", len(result), n2+overlap)
	fmt.Printf("  First 10: ")
	for i := 0; i < 10 && i < len(result); i++ {
		fmt.Printf("%.6f ", result[i])
	}
	fmt.Printf("\n  [950:960]: ")
	for i := 950; i < 960 && i < len(result); i++ {
		fmt.Printf("%.6f ", result[i])
	}
	fmt.Printf("\n  Last 10 (overlap): ")
	for i := n2; i < n2+10 && i < len(result); i++ {
		fmt.Printf("%.6f ", result[i])
	}
	fmt.Printf("\n")

	// Check that values are NOT linearly growing (the bug in old implementation)
	// For a DC impulse, output should be roughly constant or have periodic structure
	var maxVal, minVal float64 = result[0], result[0]
	for _, v := range result[:n2] {
		if v > maxVal {
			maxVal = v
		}
		if v < minVal {
			minVal = v
		}
	}
	fmt.Printf("\n  Range in first %d samples: [%.6f, %.6f]\n", n2, minVal, maxVal)

	// The old broken implementation had values from -0.0008 to -1.0 (linear growth)
	// A correct implementation should NOT have such linear growth
	// Check if last values are dramatically larger than first values
	firstAvg := 0.0
	lastAvg := 0.0
	for i := 0; i < 10; i++ {
		firstAvg += math.Abs(result[i])
		lastAvg += math.Abs(result[n2-10+i])
	}
	firstAvg /= 10
	lastAvg /= 10

	ratio := lastAvg / (firstAvg + 1e-10)
	fmt.Printf("  Avg magnitude first 10: %.6f, last 10: %.6f, ratio: %.2f\n", firstAvg, lastAvg, ratio)

	// If ratio is extremely high (like 1000x), something is wrong
	if ratio > 100 {
		t.Errorf("Suspicious linear growth detected: ratio = %.2f", ratio)
	}
}

func TestLibopusIMDCT_CompareWithDirect(t *testing.T) {
	// Compare libopus implementation with direct O(n²) IMDCT
	n2 := 960
	spectrum := make([]float64, n2)
	spectrum[0] = 1.0

	overlap := 120
	prevOverlap := make([]float64, overlap)

	// Run both implementations
	libopusResult := libopusIMDCT(spectrum, prevOverlap, overlap)
	directResult := IMDCTDirect(spectrum)

	fmt.Printf("\nComparing libopusIMDCT with IMDCTDirect:\n")
	fmt.Printf("  Direct IMDCT first 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", directResult[i])
	}
	fmt.Printf("\n  libopus IMDCT first 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", libopusResult[i])
	}
	fmt.Printf("\n")

	// The libopus IMDCT should produce values in a similar range
	// (they may differ due to different normalization and folding)
	var directEnergy, libopusEnergy float64
	for i := 0; i < n2; i++ {
		directEnergy += directResult[i] * directResult[i]
		libopusEnergy += libopusResult[i] * libopusResult[i]
	}
	fmt.Printf("\n  Energy (direct first N): %.6f\n", directEnergy)
	fmt.Printf("  Energy (libopus first N): %.6f\n", libopusEnergy)
	fmt.Printf("  Ratio: %.2f\n", libopusEnergy/directEnergy)

	// Energy ratio should be reasonable (not millions like the broken implementation)
	ratio := libopusEnergy / directEnergy
	if ratio > 1000 {
		t.Errorf("Energy ratio too high: %.2f (indicates broken implementation)", ratio)
	}
}

func TestLibopusIMDCT_Sinusoid(t *testing.T) {
	// Test with sinusoidal input
	n2 := 960
	spectrum := make([]float64, n2)
	for i := 0; i < n2; i++ {
		spectrum[i] = math.Sin(float64(i)*0.1) * 0.1
	}

	overlap := 120
	prevOverlap := make([]float64, overlap)

	result := libopusIMDCT(spectrum, prevOverlap, overlap)

	// Check for reasonable values
	var maxAbs float64
	for _, v := range result[:n2] {
		if math.Abs(v) > maxAbs {
			maxAbs = math.Abs(v)
		}
	}

	fmt.Printf("\nSinusoid input test:\n")
	fmt.Printf("  Max absolute value: %.6f\n", maxAbs)
	fmt.Printf("  First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.4f ", result[i])
	}
	fmt.Printf("\n")

	// Values shouldn't explode
	if maxAbs > 100 {
		t.Errorf("Max value too large: %.2f", maxAbs)
	}
}
