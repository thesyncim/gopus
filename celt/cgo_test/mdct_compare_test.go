//go:build cgo_libopus
// +build cgo_libopus

package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
)

func TestMDCTForwardComparison(t *testing.T) {
	// Generate a simple test signal
	frameSize := 960
	overlap := 120
	totalLen := frameSize + overlap

	// Generate a 440Hz sine wave
	sampleRate := 48000.0
	freq := 440.0
	samples := make([]float64, totalLen)
	for i := 0; i < totalLen; i++ {
		samples[i] = 0.5 * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
	}

	// Compute MDCT using gopus
	coeffs := celt.MDCTForwardWithOverlap(samples, overlap)

	// Since we can't directly access libopus MDCT, let's at least verify
	// the coefficients have the expected properties

	t.Logf("Input samples: %d (frameSize=%d, overlap=%d)", len(samples), frameSize, overlap)
	t.Logf("MDCT coefficients: %d", len(coeffs))

	// For a pure sine wave at 440Hz at 48kHz:
	// - The fundamental frequency bin should be at k = 440 * frameSize / 48000 = 8.8
	// - So we expect energy around bins 8-9

	// Find the peak bin
	maxVal := 0.0
	maxBin := 0
	totalEnergy := 0.0
	for i, c := range coeffs {
		energy := c * c
		totalEnergy += energy
		if math.Abs(c) > maxVal {
			maxVal = math.Abs(c)
			maxBin = i
		}
	}

	expectedBin := int(freq * float64(frameSize) / sampleRate)
	t.Logf("Expected peak bin: ~%d (440Hz * 960 / 48000)", expectedBin)
	t.Logf("Actual peak bin: %d with value %.6f", maxBin, maxVal)
	t.Logf("Total energy: %.6f", totalEnergy)

	// Show the first 20 coefficients
	t.Log("First 20 MDCT coefficients:")
	for i := 0; i < 20 && i < len(coeffs); i++ {
		t.Logf("  [%2d] = %10.6f", i, coeffs[i])
	}

	// Show coefficients around the expected peak
	t.Logf("Coefficients around expected peak (bins %d-%d):", expectedBin-2, expectedBin+2)
	for i := maxIntMDCT(0, expectedBin-2); i <= minIntMDCT(len(coeffs)-1, expectedBin+2); i++ {
		marker := ""
		if i == maxBin {
			marker = " <-- peak"
		}
		t.Logf("  [%2d] = %10.6f%s", i, coeffs[i], marker)
	}
}

func TestMDCTRoundtrip(t *testing.T) {
	// Test that MDCT -> IMDCT gives back the original signal
	// (with proper windowing and overlap-add)

	frameSize := 960
	overlap := 120
	totalLen := frameSize + overlap

	// Generate a simple test signal
	sampleRate := 48000.0
	freq := 440.0
	samples := make([]float64, totalLen)
	for i := 0; i < totalLen; i++ {
		samples[i] = 0.5 * math.Sin(2*math.Pi*freq*float64(i)/sampleRate)
	}

	// Apply window to input (for proper analysis)
	window := celt.GetWindowBuffer(overlap)

	// Compute forward MDCT
	coeffs := celt.MDCTForwardWithOverlap(samples, overlap)

	// Compute inverse MDCT
	reconstructed := celt.IMDCT(coeffs)

	// The IMDCT output should match the original (windowed) signal
	// after proper overlap handling

	t.Logf("Original samples: %d", len(samples))
	t.Logf("MDCT coefficients: %d", len(coeffs))
	t.Logf("IMDCT output: %d", len(reconstructed))

	// Compare middle portion (avoiding overlap regions)
	t.Log("Comparison of middle samples:")
	t.Logf("  idx     original    reconstructed   diff")
	midStart := overlap
	for i := midStart; i < midStart+10; i++ {
		orig := samples[i]
		recon := 0.0
		if i < len(reconstructed) {
			recon = reconstructed[i]
		}
		diff := orig - recon
		t.Logf("  [%3d]  %10.6f    %10.6f    %10.6f", i, orig, recon, diff)
	}

	// Check correlation in middle portion
	midEnd := len(samples) - overlap
	if midEnd > midStart+10 {
		var sumOrig, sumRecon, sumOrigRecon float64
		var sumOrigSq, sumReconSq float64
		count := 0
		for i := midStart; i < midEnd && i < len(reconstructed); i++ {
			o := samples[i]
			r := reconstructed[i]
			sumOrig += o
			sumRecon += r
			sumOrigRecon += o * r
			sumOrigSq += o * o
			sumReconSq += r * r
			count++
		}
		if count > 0 {
			n := float64(count)
			num := n*sumOrigRecon - sumOrig*sumRecon
			den := math.Sqrt((n*sumOrigSq - sumOrig*sumOrig) * (n*sumReconSq - sumRecon*sumRecon))
			corr := 0.0
			if den > 0 {
				corr = num / den
			}
			t.Logf("Correlation in middle region: %.4f", corr)

			// CRITICAL: Check for sign inversion
			if corr < -0.9 {
				t.Errorf("SIGNAL INVERTED! Correlation = %.4f", corr)
			} else if corr < 0.9 {
				t.Errorf("Poor correlation: %.4f (expected > 0.9)", corr)
			}
		}
	}

	_ = window // window used for reference
}

func minIntMDCT(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxIntMDCT(a, b int) int {
	if a > b {
		return a
	}
	return b
}
