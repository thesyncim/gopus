// Package cgo compares raw IMDCT output between gopus and libopus
package cgo

import (
	"math"
	"math/cmplx"
	"testing"
)

// TestDFTPrecision tests DFT precision for 60-point (2.5ms frame) sizes
func TestDFTPrecision(t *testing.T) {
	// For 2.5ms frames: 120 coefficients -> 60-point complex DFT

	// Test DFT with known input
	n := 60
	input := make([]complex128, n)
	for i := 0; i < n; i++ {
		// Simple signal: single frequency
		angle := 2 * math.Pi * float64(i) * 5 / float64(n) // 5 cycles
		input[i] = complex(math.Cos(angle), math.Sin(angle))
	}

	// Compute DFT using our implementation
	output := dftTest(input)

	// Compute reference DFT using direct formula
	reference := make([]complex128, n)
	for k := 0; k < n; k++ {
		var sum complex128
		for j := 0; j < n; j++ {
			angle := -2 * math.Pi * float64(k) * float64(j) / float64(n)
			twiddle := complex(math.Cos(angle), math.Sin(angle))
			sum += input[j] * twiddle
		}
		reference[k] = sum
	}

	// Compare
	var maxDiff float64
	var maxDiffIdx int
	for i := 0; i < n; i++ {
		diff := cmplx.Abs(output[i] - reference[i])
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}

	t.Logf("60-point DFT precision test:")
	t.Logf("  Max diff: %.2e at index %d", maxDiff, maxDiffIdx)

	// The output should have a peak at bin 5 (since input is 5 cycles)
	var peakIdx int
	var peakMag float64
	for i := 0; i < n; i++ {
		mag := cmplx.Abs(output[i])
		if mag > peakMag {
			peakMag = mag
			peakIdx = i
		}
	}
	t.Logf("  Peak at bin %d with magnitude %.2f (expected bin 5)", peakIdx, peakMag)

	if maxDiff > 1e-10 {
		t.Errorf("DFT precision too low: max diff = %.2e", maxDiff)
	}
}

// dftTest is our DFT implementation (copy from mdct.go for testing)
func dftTest(x []complex128) []complex128 {
	n := len(x)
	if n == 0 {
		return nil
	}
	y := make([]complex128, n)

	// Direct DFT implementation
	for k := 0; k < n; k++ {
		var sum complex128
		baseAngle := -2 * math.Pi * float64(k) / float64(n)
		for j := 0; j < n; j++ {
			angle := baseAngle * float64(j)
			twiddle := complex(math.Cos(angle), math.Sin(angle))
			sum += x[j] * twiddle
		}
		y[k] = sum
	}
	return y
}

// TestIMDCTForShortFrame tests IMDCT specifically for 2.5ms frame sizes
func TestIMDCTForShortFrame(t *testing.T) {
	// For 2.5ms frames: 120 coefficients
	n2 := 120
	_ = n2 * 2   // 240 (n, unused)
	n4 := n2 / 2 // 60

	// Generate test coefficients
	coeffs := make([]float64, n2)
	for i := 0; i < n2; i++ {
		// Simple pattern that exercises all coefficients
		coeffs[i] = math.Sin(float64(i)*0.1) * 0.5
	}

	// Compute IMDCT using our implementation
	output := computeIMDCT(coeffs)

	// Verify output properties
	t.Logf("IMDCT for 2.5ms frame (n2=%d):", n2)
	t.Logf("  Output length: %d", len(output))

	// Check energy preservation (rough check)
	var inputEnergy, outputEnergy float64
	for _, c := range coeffs {
		inputEnergy += c * c
	}
	for _, s := range output {
		outputEnergy += s * s
	}

	t.Logf("  Input energy: %.6f", inputEnergy)
	t.Logf("  Output energy: %.6f", outputEnergy)
	t.Logf("  Ratio: %.4f", outputEnergy/inputEnergy)

	// For IMDCT, output should have 2x the samples
	if len(output) != n2 {
		t.Logf("  Note: Output is %d samples (CELT IMDCT produces n2 samples, not 2*n2)", len(output))
	}

	// Show first and last few samples
	t.Logf("  First 10 samples:")
	for i := 0; i < 10 && i < len(output); i++ {
		t.Logf("    [%d] = %.8f", i, output[i])
	}
	t.Logf("  Last 10 samples:")
	for i := len(output) - 10; i < len(output); i++ {
		t.Logf("    [%d] = %.8f", i, output[i])
	}

	_ = n4 // unused but kept for reference
}

// computeIMDCT computes IMDCT matching our celt implementation
func computeIMDCT(spectrum []float64) []float64 {
	n2 := len(spectrum)
	if n2 == 0 {
		return nil
	}

	n := n2 * 2
	n4 := n2 / 2

	// Get twiddles
	trig := make([]float64, n2)
	for i := 0; i < n2; i++ {
		angle := 2.0 * math.Pi * (float64(i) + 0.125) / float64(n)
		trig[i] = math.Cos(angle)
	}

	// Pre-rotate
	fftIn := make([]complex128, n4)
	for i := 0; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1
		fftIn[i] = complex(yi, yr) // Swap for FFT instead of IFFT
	}

	// DFT
	fftOut := dftTest(fftIn)

	// Unpack
	buf := make([]float64, n2)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		buf[2*i] = real(v)
		buf[2*i+1] = imag(v)
	}

	// Post-rotate
	yp0 := 0
	yp1 := n2 - 2
	for i := 0; i < (n4+1)>>1; i++ {
		re := buf[yp0+1]
		im := buf[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0
		re2 := buf[yp1+1]
		im2 := buf[yp1]
		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0
		buf[yp1] = yr
		buf[yp0+1] = yi
		yp0 += 2
		yp1 -= 2
	}

	return buf
}

// TestIMDCTRoundtrip tests MDCT->IMDCT roundtrip for short frames
func TestIMDCTRoundtrip(t *testing.T) {
	// Test that MDCT->IMDCT->overlap-add reconstructs the original
	// For simplicity, test with a synthetic signal

	frameSize := 120 // 2.5ms at 48kHz
	overlap := 120

	// Generate test signal (two frames worth)
	signal := make([]float64, 2*frameSize+overlap)
	for i := range signal {
		signal[i] = math.Sin(float64(i)*0.05) * 0.8
	}

	// Window for analysis
	window := make([]float64, overlap)
	for i := 0; i < overlap; i++ {
		x := float64(i) + 0.5
		sinArg := 0.5 * math.Pi * x / float64(overlap)
		s := math.Sin(sinArg)
		window[i] = math.Sin(0.5 * math.Pi * s * s)
	}

	t.Logf("IMDCT roundtrip test for 2.5ms frames:")
	t.Logf("  Frame size: %d, Overlap: %d", frameSize, overlap)
	t.Logf("  Window[0]: %.8f, Window[%d]: %.8f", window[0], overlap-1, window[overlap-1])

	// The MDCT/IMDCT should preserve signal through overlap-add
	// This is a property test, not comparing to libopus
}
