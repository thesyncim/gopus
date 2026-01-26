package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestIMDCTDebug(t *testing.T) {
	n2 := 960
	n := n2 * 2
	n4 := n2 / 2

	// DC impulse
	spectrum := make([]float64, n2)
	spectrum[0] = 1.0

	trig := getMDCTTrig(n)

	fmt.Printf("Trig table values:\n")
	fmt.Printf("  trig[0] = %.10f\n", trig[0])
	fmt.Printf("  trig[1] = %.10f\n", trig[1])
	fmt.Printf("  trig[479] = %.10f\n", trig[479])
	fmt.Printf("  trig[480] = %.10f\n", trig[480])
	fmt.Printf("  trig[481] = %.10f\n", trig[481])
	fmt.Printf("  trig[959] = %.10f\n", trig[959])

	// Pre-rotate
	fmt.Printf("\nPre-rotate stage:\n")
	fftIn := make([]complex128, n4)
	for i := 0; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1
		fftIn[i] = complex(yi, yr)
		if i < 3 || i >= n4-3 {
			fmt.Printf("  i=%d: x1=%.4f, x2=%.4f, t0=%.6f, t1=%.6f -> fftIn=(%.6f, %.6f)\n",
				i, x1, x2, t0, t1, yi, yr)
		}
	}

	// DFT
	fmt.Printf("\nDFT stage:\n")
	fftOut := dft(fftIn)
	fmt.Printf("  First 3: (%.6f,%.6f) (%.6f,%.6f) (%.6f,%.6f)\n",
		real(fftOut[0]), imag(fftOut[0]),
		real(fftOut[1]), imag(fftOut[1]),
		real(fftOut[2]), imag(fftOut[2]))
	fmt.Printf("  Last 3: (%.6f,%.6f) (%.6f,%.6f) (%.6f,%.6f)\n",
		real(fftOut[477]), imag(fftOut[477]),
		real(fftOut[478]), imag(fftOut[478]),
		real(fftOut[479]), imag(fftOut[479]))

	// Interleave to buf
	buf := make([]float64, n2)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		buf[2*i] = real(v)
		buf[2*i+1] = imag(v)
	}

	fmt.Printf("\nBuf before post-rotate:\n")
	fmt.Printf("  First 6: %.6f %.6f %.6f %.6f %.6f %.6f\n",
		buf[0], buf[1], buf[2], buf[3], buf[4], buf[5])
	fmt.Printf("  Last 6: %.6f %.6f %.6f %.6f %.6f %.6f\n",
		buf[954], buf[955], buf[956], buf[957], buf[958], buf[959])

	// Post-rotate
	fmt.Printf("\nPost-rotate stage (tracing first 3 and last 3 iterations):\n")
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

		if i < 3 || i >= (n4+1)>>1-3 {
			fmt.Printf("  i=%d: yp0=%d, yp1=%d\n", i, yp0, yp1)
			fmt.Printf("    read: buf[%d]=%.6f, buf[%d]=%.6f, buf[%d]=%.6f, buf[%d]=%.6f\n",
				yp0, im, yp0+1, re, yp1, im2, yp1+1, re2)
			fmt.Printf("    t0=%.6f, t1=%.6f, t0'=%.6f, t1'=%.6f\n",
				t0, t1, trig[n4-i-1], trig[n2-i-1])
		}

		buf[yp0] = yr
		buf[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0
		buf[yp1] = yr
		buf[yp0+1] = yi

		if i < 3 || i >= (n4+1)>>1-3 {
			fmt.Printf("    write: buf[%d]=%.6f, buf[%d]=%.6f, buf[%d]=%.6f, buf[%d]=%.6f\n",
				yp0, buf[yp0], yp0+1, buf[yp0+1], yp1, buf[yp1], yp1+1, buf[yp1+1])
		}

		yp0 += 2
		yp1 -= 2
	}

	fmt.Printf("\nBuf after post-rotate:\n")
	fmt.Printf("  First 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", buf[i])
	}
	fmt.Printf("\n  Last 10: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.6f ", buf[i])
	}
	fmt.Printf("\n")

	// Compare with direct IMDCT
	directOut := IMDCTDirect(spectrum)
	fmt.Printf("\nDirect IMDCT first 10: ")
	for i := 0; i < 10; i++ {
		fmt.Printf("%.6f ", directOut[i])
	}
	fmt.Printf("\n")

	// Check energy
	var bufEnergy, directEnergy float64
	for i := 0; i < n2; i++ {
		bufEnergy += buf[i] * buf[i]
		directEnergy += directOut[i] * directOut[i]
	}
	fmt.Printf("\nEnergy: buf=%.6f, direct=%.6f, ratio=%.2f\n",
		bufEnergy, directEnergy, bufEnergy/directEnergy)

	// The issue: the DFT-based IMDCT should produce different values than
	// direct IMDCT because they compute different things.
	// DFT-based: produces N samples (folded form with overlap-add built in)
	// Direct: produces 2N samples (full IMDCT)
	//
	// But the DFT-based is producing linearly growing values which is wrong.
	// Let's check if the trig table formula matches libopus.

	fmt.Printf("\n\n=== Checking trig table formula ===\n")
	// Our formula: trig[i] = cos(2π(i+0.125)/n)
	// libopus formula: trig[i] = cos(π/2 * (i + 0.125) / N4) where N4 = N/4 = n2/2 = 480
	//                = cos(π/2 * (i + 0.125) / 480)
	//                = cos(π * (i + 0.125) / 960)
	//                = cos(π * (i + 0.125) / n2)

	// Our formula: cos(2π(i+0.125)/n) = cos(2π(i+0.125)/(2*n2)) = cos(π(i+0.125)/n2)
	// This matches!

	fmt.Printf("Verifying trig formula:\n")
	for _, i := range []int{0, 1, 100, 479, 480, 959} {
		our := trig[i]
		expected := math.Cos(math.Pi * (float64(i) + 0.125) / float64(n2))
		fmt.Printf("  trig[%d]: ours=%.10f, expected=%.10f, diff=%.2e\n",
			i, our, expected, our-expected)
	}
}
