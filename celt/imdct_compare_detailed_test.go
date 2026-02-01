package celt

import (
	"fmt"
	"math"
	"testing"
)

func TestIMDCTDirectVsDFT(t *testing.T) {
	// Compare IMDCTDirect with the DFT-based version used in imdctOverlapWithPrev
	N := 480
	spectrum := make([]float64, N)
	for i := 0; i < N; i++ {
		spectrum[i] = math.Sin(float64(i) * 0.1)
	}

	// Get direct result
	direct := IMDCTDirect(spectrum)
	fmt.Printf("IMDCTDirect produces %d samples\n", len(direct))

	// Get DFT-based result (extract the internal buf before windowing)
	// Recreate what imdctOverlapWithPrev does internally
	n2 := len(spectrum) // 480
	n := n2 * 2         // 960
	n4 := n2 / 2        // 240

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
	buf := make([]float64, n2)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		buf[2*i] = real(v)
		buf[2*i+1] = imag(v)
	}

	// Post-processing
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

	fmt.Printf("DFT-based buf produces %d samples\n", len(buf))

	// Compare buf with appropriate section of direct output
	// The DFT-based IMDCT should match the first N samples of the full IMDCT
	// after some transformation
	fmt.Printf("\n=== Comparing DFT buf with IMDCTDirect ===\n")
	fmt.Printf("Pos | DFT buf | Direct[pos] | Direct[pos+N] | Diff (buf - direct)\n")

	maxDiff := 0.0
	maxDiffPos := 0
	for i := 0; i < 20; i++ {
		diff := buf[i] - direct[i]
		if math.Abs(diff) > maxDiff {
			maxDiff = math.Abs(diff)
			maxDiffPos = i
		}
		fmt.Printf("%3d | %10.6f | %10.6f | %10.6f | %10.6f\n",
			i, buf[i], direct[i], direct[i+N], diff)
	}
	fmt.Println("...")
	for i := N - 10; i < N; i++ {
		diff := buf[i] - direct[i]
		if math.Abs(diff) > maxDiff {
			maxDiff = math.Abs(diff)
			maxDiffPos = i
		}
		fmt.Printf("%3d | %10.6f | %10.6f | %10.6f | %10.6f\n",
			i, buf[i], direct[i], direct[i+N], diff)
	}
	fmt.Printf("\nMax difference: %.10f at position %d\n", maxDiff, maxDiffPos)

	// Also check if buf corresponds to any linear combination
	fmt.Printf("\n=== Checking if buf = direct[0:N] + direct[N:2N] ===\n")
	for i := 0; i < 10; i++ {
		sum := direct[i] + direct[i+N]
		fmt.Printf("%3d | buf=%.6f | sum=%.6f | diff=%.6f\n", i, buf[i], sum, buf[i]-sum)
	}

	// Check against negative sum
	fmt.Printf("\n=== Checking if buf = direct[0:N] - direct[2N-1:N-1:-1] (TDAC folding) ===\n")
	for i := 0; i < 10; i++ {
		folded := direct[i] - direct[2*N-1-i]
		fmt.Printf("%3d | buf=%.6f | folded=%.6f | diff=%.6f\n", i, buf[i], folded, buf[i]-folded)
	}
}
