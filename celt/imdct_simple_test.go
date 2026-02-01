package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestIMDCTSimple(t *testing.T) {
	// Test with a simple DC input (all ones)
	n2 := 960
	spectrum := make([]float64, n2)
	spectrum[0] = 1.0 // Single DC coefficient

	// Direct IMDCT
	directOut := IMDCTDirect(spectrum)
	fmt.Printf("Direct IMDCT of DC impulse:\n")
	fmt.Printf("  First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", directOut[i])
	}
	fmt.Printf("\n  [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.6f ", directOut[i])
	}
	fmt.Printf("\n  [1910:1920]: ")
	for i := 1910; i < 1920; i++ {
		fmt.Printf("%.6f ", directOut[i])
	}
	fmt.Printf("\n")

	// DFT-based IMDCT (from imdctOverlapWithPrev)
	n := n2 * 2
	n4 := n2 / 2

	trig := getMDCTTrig(n)
	fftIn := make([]complex128, n4)
	for i := 0; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1
		fftIn[i] = complex(yi, yr)
	}

	fftOut := dft(fftIn)
	bufDFT := make([]float64, n2)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		bufDFT[2*i] = real(v)
		bufDFT[2*i+1] = imag(v)
	}

	yp0 := 0
	yp1 := n2 - 2
	for i := 0; i < (n4+1)>>1; i++ {
		re := bufDFT[yp0+1]
		im := bufDFT[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0
		re2 := bufDFT[yp1+1]
		im2 := bufDFT[yp1]
		bufDFT[yp0] = yr
		bufDFT[yp1+1] = yi
		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0
		bufDFT[yp1] = yr
		bufDFT[yp0+1] = yi
		yp0 += 2
		yp1 -= 2
	}

	fmt.Printf("\nDFT-based IMDCT of DC impulse:\n")
	fmt.Printf("  First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", bufDFT[i])
	}
	fmt.Printf("\n  [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.6f ", bufDFT[i])
	}
	fmt.Printf("\n")

	// Check energy preservation
	var energyDirect, energyDFT float64
	for i := 0; i < n2; i++ {
		energyDirect += directOut[i] * directOut[i]
		energyDFT += bufDFT[i] * bufDFT[i]
	}
	fmt.Printf("\nEnergy comparison (first N samples):\n")
	fmt.Printf("  Direct: %.6f\n", energyDirect)
	fmt.Printf("  DFT-based: %.6f\n", energyDFT)
	fmt.Printf("  Ratio (DFT/Direct): %.6f\n", energyDFT/energyDirect)

	// Check max values
	var maxDirect, maxDFT float64
	for i := 0; i < n2; i++ {
		if math.Abs(directOut[i]) > maxDirect {
			maxDirect = math.Abs(directOut[i])
		}
		if math.Abs(bufDFT[i]) > maxDFT {
			maxDFT = math.Abs(bufDFT[i])
		}
	}
	fmt.Printf("\nMax absolute value:\n")
	fmt.Printf("  Direct: %.6f\n", maxDirect)
	fmt.Printf("  DFT-based: %.6f\n", maxDFT)

	// Test with a sinusoid input
	fmt.Printf("\n\n=== Testing with sinusoid input ===\n")
	for i := 0; i < n2; i++ {
		spectrum[i] = math.Sin(float64(i)*0.1) * 0.1
	}

	directOut = IMDCTDirect(spectrum)

	// Recompute DFT-based
	for i := 0; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1
		fftIn[i] = complex(yi, yr)
	}

	fftOut = dft(fftIn)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		bufDFT[2*i] = real(v)
		bufDFT[2*i+1] = imag(v)
	}

	yp0 = 0
	yp1 = n2 - 2
	for i := 0; i < (n4+1)>>1; i++ {
		re := bufDFT[yp0+1]
		im := bufDFT[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := re*t0 + im*t1
		yi := re*t1 - im*t0
		re2 := bufDFT[yp1+1]
		im2 := bufDFT[yp1]
		bufDFT[yp0] = yr
		bufDFT[yp1+1] = yi
		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0
		bufDFT[yp1] = yr
		bufDFT[yp0+1] = yi
		yp0 += 2
		yp1 -= 2
	}

	fmt.Printf("Direct IMDCT:\n")
	fmt.Printf("  First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.4f ", directOut[i])
	}
	fmt.Printf("\n  [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.4f ", directOut[i])
	}
	fmt.Printf("\n")

	fmt.Printf("\nDFT-based IMDCT:\n")
	fmt.Printf("  First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.4f ", bufDFT[i])
	}
	fmt.Printf("\n  [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.4f ", bufDFT[i])
	}
	fmt.Printf("\n")

	// Compare
	var maxDiff float64
	maxDiffIdx := 0
	for i := 0; i < n2; i++ {
		diff := math.Abs(bufDFT[i] - directOut[i])
		if diff > maxDiff {
			maxDiff = diff
			maxDiffIdx = i
		}
	}
	fmt.Printf("\nMax difference: %.4f at index %d\n", maxDiff, maxDiffIdx)
}
