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

	start := overlap / 2
	end := start + n2
	if start < 0 {
		start = 0
	}
	if end > len(result) {
		end = len(result)
	}
	signal := result[start:end]

	fmt.Printf("libopusIMDCT DC impulse test:\n")
	fmt.Printf("  Output length: %d (expected %d)\n", len(result), n2+overlap)
	fmt.Printf("  IMDCT region start: %d, len: %d\n", start, len(signal))
	fmt.Printf("  IMDCT first 10: ")
	for i := 0; i < 10 && i < len(signal); i++ {
		fmt.Printf("%.6f ", signal[i])
	}
	fmt.Printf("\n  IMDCT last 10: ")
	for i := len(signal) - 10; i < len(signal) && i >= 0; i++ {
		fmt.Printf("%.6f ", signal[i])
	}
	fmt.Printf("\n")

	// Check that values are not perfectly linear across the IMDCT region.
	// The old broken implementation produced a near-linear ramp.
	if len(signal) > 2 {
		first := signal[0]
		last := signal[len(signal)-1]
		var errpow, sigpow float64
		for i := 0; i < len(signal); i++ {
			lin := first + (last-first)*float64(i)/float64(len(signal)-1)
			diff := signal[i] - lin
			errpow += diff * diff
			sigpow += signal[i] * signal[i]
		}
		if sigpow > 0 {
			linearity := errpow / sigpow
			fmt.Printf("  Linearity residual ratio: %.6f\n", linearity)
			if linearity < 1e-4 {
				t.Errorf("Suspiciously linear output detected (residual ratio=%.6f)", linearity)
			}
		}
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
