package celt

import (
	"fmt"
	"math"
	"math/cmplx"
	"testing"
)

// idft computes inverse DFT
func idft(x []complex128) []complex128 {
	n := len(x)
	if n <= 1 {
		return x
	}

	out := make([]complex128, n)
	twoPi := 2.0 * math.Pi / float64(n) // Positive for IDFT
	scale := 1.0 / float64(n)           // Normalization

	for k := 0; k < n; k++ {
		angle := twoPi * float64(k)
		wStep := complex(math.Cos(angle), math.Sin(angle))
		w := complex(1.0, 0.0)
		var sum complex128
		for t := 0; t < n; t++ {
			sum += x[t] * w
			w *= wStep
		}
		out[k] = sum * complex(scale, 0)
	}
	return out
}

func TestIDFTVsDFT(t *testing.T) {
	// Test that IDFT is actually the inverse of DFT
	n := 8
	x := make([]complex128, n)
	x[0] = complex(1, 0) // impulse

	dftOut := dft(x)
	idftOut := idft(dftOut)

	fmt.Println("Impulse -> DFT -> IDFT:")
	fmt.Printf("Original: ")
	for _, v := range x {
		fmt.Printf("(%.2f,%.2f) ", real(v), imag(v))
	}
	fmt.Printf("\nAfter DFT: ")
	for _, v := range dftOut {
		fmt.Printf("(%.2f,%.2f) ", real(v), imag(v))
	}
	fmt.Printf("\nAfter IDFT: ")
	for _, v := range idftOut {
		fmt.Printf("(%.2f,%.2f) ", real(v), imag(v))
	}
	fmt.Println()

	// Check if IDFT recovers original
	maxErr := 0.0
	for i := 0; i < n; i++ {
		err := cmplx.Abs(idftOut[i] - x[i])
		if err > maxErr {
			maxErr = err
		}
	}
	fmt.Printf("Max error: %.6e\n", maxErr)
}

func TestIMDCTWithIDFT(t *testing.T) {
	// Try using IDFT instead of DFT in the IMDCT
	n2 := 960
	n := n2 * 2
	n4 := n2 / 2

	spectrum := make([]float64, n2)
	spectrum[0] = 1.0 // DC impulse

	trig := getMDCTTrig(n)

	// Pre-rotate WITHOUT real/imag swap
	fftIn := make([]complex128, n4)
	for i := 0; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1
		// Normal order (not swapped)
		fftIn[i] = complex(yr, yi)
	}

	// Use IDFT instead of DFT
	fftOut := idft(fftIn)

	buf := make([]float64, n2)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		buf[2*i] = real(v)
		buf[2*i+1] = imag(v)
	}

	// Post-rotate WITHOUT swap
	yp0 := 0
	yp1 := n2 - 2
	for i := 0; i < (n4+1)>>1; i++ {
		re := buf[yp0]   // Normal order
		im := buf[yp0+1] // Normal order
		t0 := trig[i]
		t1 := trig[n4+i]

		yr := re*t0 + im*t1
		yi := im*t0 - re*t1 // Note: different formula

		re2 := buf[yp1]
		im2 := buf[yp1+1]

		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]

		yr = re2*t0 + im2*t1
		yi = im2*t0 - re2*t1

		buf[yp1] = yr
		buf[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}

	fmt.Println("\n=== IMDCT with IDFT (no swap) ===")
	fmt.Printf("First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", buf[i])
	}
	fmt.Printf("\nLast 10: ")
	for i := n2 - 10; i < n2; i++ {
		fmt.Printf("%.6f ", buf[i])
	}
	fmt.Println()

	// Compare with Direct IMDCT
	directOut := IMDCTDirect(spectrum)
	fmt.Printf("\nDirect first 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", directOut[i])
	}
	fmt.Printf("\nDirect last 10: ")
	for i := n2 - 10; i < n2; i++ {
		fmt.Printf("%.6f ", directOut[i])
	}
	fmt.Println()

	// Check energy
	var bufEnergy, directEnergy float64
	for i := 0; i < n2; i++ {
		bufEnergy += buf[i] * buf[i]
		directEnergy += directOut[i] * directOut[i]
	}
	fmt.Printf("\nEnergy: IDFT-based=%.6f, Direct=%.6f, ratio=%.2f\n",
		bufEnergy, directEnergy, bufEnergy/directEnergy)
}
