package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestIMDCTTraceSteps(t *testing.T) {
	// Use DC impulse to trace through each step
	n2 := 960
	n := n2 * 2
	n4 := n2 / 2

	spectrum := make([]float64, n2)
	spectrum[0] = 1.0

	trig := getMDCTTrig(n)

	// Step 1: Pre-rotate
	fmt.Println("=== Step 1: Pre-rotate ===")
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

	fmt.Printf("fftIn first 5: ")
	for i := 0; i < 5; i++ {
		fmt.Printf("(%.6f,%.6f) ", real(fftIn[i]), imag(fftIn[i]))
	}
	fmt.Printf("\nfftIn last 5: ")
	for i := n4 - 5; i < n4; i++ {
		fmt.Printf("(%.6f,%.6f) ", real(fftIn[i]), imag(fftIn[i]))
	}
	fmt.Println()

	// Only fftIn[0] should be non-zero
	fmt.Printf("Non-zero inputs: ")
	for i := 0; i < n4; i++ {
		if real(fftIn[i]) != 0 || imag(fftIn[i]) != 0 {
			fmt.Printf("fftIn[%d]=(%.6f,%.6f) ", i, real(fftIn[i]), imag(fftIn[i]))
		}
	}
	fmt.Println()

	// Step 2: DFT
	fmt.Println("\n=== Step 2: DFT ===")
	fftOut := dft(fftIn)

	fmt.Printf("fftOut first 5: ")
	for i := 0; i < 5; i++ {
		fmt.Printf("(%.6f,%.6f) ", real(fftOut[i]), imag(fftOut[i]))
	}
	fmt.Printf("\nfftOut last 5: ")
	for i := n4 - 5; i < n4; i++ {
		fmt.Printf("(%.6f,%.6f) ", real(fftOut[i]), imag(fftOut[i]))
	}
	fmt.Println()

	// For a single non-zero input, DFT output should all be equal
	allEqual := true
	for i := 1; i < n4; i++ {
		if math.Abs(real(fftOut[i])-real(fftOut[0])) > 1e-10 ||
			math.Abs(imag(fftOut[i])-imag(fftOut[0])) > 1e-10 {
			allEqual = false
			break
		}
	}
	fmt.Printf("All DFT outputs equal: %v\n", allEqual)

	// Step 3: Interleave to buf
	fmt.Println("\n=== Step 3: Interleave to buf ===")
	buf := make([]float64, n2)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		buf[2*i] = real(v)
		buf[2*i+1] = imag(v)
	}

	fmt.Printf("buf first 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", buf[i])
	}
	fmt.Printf("\nbuf last 10: ")
	for i := n2 - 10; i < n2; i++ {
		fmt.Printf("%.6f ", buf[i])
	}
	fmt.Println()

	// Step 4: Post-rotate - TRACE EACH ITERATION
	fmt.Println("\n=== Step 4: Post-rotate (first few iterations) ===")
	yp0 := 0
	yp1 := n2 - 2

	// Make a copy to trace
	bufOrig := make([]float64, n2)
	copy(bufOrig, buf)

	for i := 0; i < (n4+1)>>1; i++ {
		re := buf[yp0+1]
		im := buf[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]

		yr := re*t0 + im*t1
		yi := re*t1 - im*t0

		re2 := buf[yp1+1]
		im2 := buf[yp1]

		if i < 3 || i >= (n4+1)>>1-3 {
			fmt.Printf("i=%d: yp0=%d, yp1=%d\n", i, yp0, yp1)
			fmt.Printf("  read: buf[%d]=%.6f, buf[%d]=%.6f, buf[%d]=%.6f, buf[%d]=%.6f\n",
				yp0, im, yp0+1, re, yp1, im2, yp1+1, re2)
			fmt.Printf("  t[%d]=%.6f, t[%d]=%.6f\n", i, t0, n4+i, t1)
			fmt.Printf("  yr1=%.6f, yi1=%.6f\n", yr, yi)
		}

		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]

		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0

		if i < 3 || i >= (n4+1)>>1-3 {
			fmt.Printf("  t[%d]=%.6f, t[%d]=%.6f\n", n4-i-1, t0, n2-i-1, t1)
			fmt.Printf("  yr2=%.6f, yi2=%.6f\n", yr, yi)
			fmt.Printf("  write: buf[%d]=%.6f, buf[%d]=%.6f, buf[%d]=%.6f, buf[%d]=%.6f\n",
				yp0, buf[yp0], yp0+1, yi, yp1, yr, yp1+1, buf[yp1+1])
		}

		buf[yp1] = yr
		buf[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}

	fmt.Printf("\nbuf after post-rotate first 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", buf[i])
	}
	fmt.Printf("\nbuf after post-rotate last 10: ")
	for i := n2 - 10; i < n2; i++ {
		fmt.Printf("%.6f ", buf[i])
	}
	fmt.Println()

	// Compare with Direct IMDCT
	fmt.Println("\n=== Comparison with Direct IMDCT ===")
	directOut := IMDCTDirect(spectrum)

	fmt.Printf("Direct first 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", directOut[i])
	}
	fmt.Printf("\nDirect last 10: ")
	for i := n2 - 10; i < n2; i++ {
		fmt.Printf("%.6f ", directOut[i])
	}
	fmt.Println()

	// Show the ratio at each position
	fmt.Println("\n=== Ratio DFT/Direct at end ===")
	for i := n2 - 10; i < n2; i++ {
		if math.Abs(directOut[i]) > 1e-10 {
			fmt.Printf("[%d] DFT=%.6f, Direct=%.6f, ratio=%.2f\n",
				i, buf[i], directOut[i], buf[i]/directOut[i])
		}
	}
}
