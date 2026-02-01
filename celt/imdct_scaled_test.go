package celt

import (
	"fmt"
	"math"
	"testing"
)

// imdctOverlapWithPrevScaled is like imdctOverlapWithPrev but with 1/N4 scaling
func imdctOverlapWithPrevScaled(spectrum []float64, prevOverlap []float64, overlap int) []float64 {
	n2 := len(spectrum)
	if n2 == 0 {
		return nil
	}
	if overlap < 0 {
		overlap = 0
	}

	n := n2 * 2
	n4 := n2 / 2
	out := make([]float64, n2+overlap)

	if overlap > 0 && len(prevOverlap) > 0 {
		copyLen := overlap
		if len(prevOverlap) < copyLen {
			copyLen = len(prevOverlap)
		}
		copy(out[:copyLen], prevOverlap[:copyLen])
	}

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

	// Apply 1/N4 scaling
	scale := 1.0 / float64(n4)
	buf := make([]float64, n2)
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		buf[2*i] = real(v) * scale
		buf[2*i+1] = imag(v) * scale
	}

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

	start := overlap / 2
	if start+n2 <= len(out) {
		copy(out[start:start+n2], buf)
	}

	if overlap > 0 {
		window := GetWindowBuffer(overlap)
		xp1 := overlap - 1
		yp1 := 0
		wp1 := 0
		wp2 := overlap - 1
		for i := 0; i < overlap/2; i++ {
			x1 := out[xp1]
			x2 := out[yp1]
			out[yp1] = x2*window[wp2] - x1*window[wp1]
			out[xp1] = x2*window[wp1] + x1*window[wp2]
			yp1++
			xp1--
			wp1++
			wp2--
		}
	}

	return out
}

func TestIMDCTScaled(t *testing.T) {
	n2 := 960
	spectrum := make([]float64, n2)
	spectrum[0] = 1.0

	overlap := 120
	prevOverlap := make([]float64, overlap)

	// Test scaled version
	scaled := imdctOverlapWithPrevScaled(spectrum, prevOverlap, overlap)
	unscaled := imdctOverlapWithPrev(spectrum, prevOverlap, overlap)
	direct := IMDCTDirect(spectrum)

	fmt.Println("=== DC impulse comparison ===")

	fmt.Printf("\nScaled DFT-based [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.6f ", scaled[i])
	}

	fmt.Printf("\nUnscaled DFT-based [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.6f ", unscaled[i])
	}

	fmt.Printf("\nDirect IMDCT [950:960]: ")
	for i := 950; i < 960; i++ {
		fmt.Printf("%.6f ", direct[i])
	}
	fmt.Println()

	// Check energies
	var scaledE, unscaledE, directE float64
	for i := 0; i < n2; i++ {
		scaledE += scaled[i] * scaled[i]
		unscaledE += unscaled[i] * unscaled[i]
		directE += direct[i] * direct[i]
	}
	fmt.Printf("\nEnergy: scaled=%.6f, unscaled=%.6f, direct=%.6f\n",
		scaledE, unscaledE, directE)
	fmt.Printf("Ratio scaled/direct: %.2f\n", scaledE/directE)
}

func TestIMDCTScaledRealistic(t *testing.T) {
	n2 := 960
	spectrum := make([]float64, n2)
	for i := 0; i < n2; i++ {
		spectrum[i] = math.Sin(float64(i)*0.3) * math.Exp(-float64(i)/200.0)
	}

	overlap := 120
	prevOverlap := make([]float64, overlap)

	scaled := imdctOverlapWithPrevScaled(spectrum, prevOverlap, overlap)

	var firstAvg, lastAvg float64
	for i := 0; i < 10; i++ {
		firstAvg += math.Abs(scaled[i])
		lastAvg += math.Abs(scaled[n2-10+i])
	}
	firstAvg /= 10
	lastAvg /= 10

	fmt.Println("\n=== Realistic spectrum with scaling ===")
	fmt.Printf("Scaled: first avg=%.6f, last avg=%.6f, ratio=%.2f\n",
		firstAvg, lastAvg, lastAvg/(firstAvg+1e-10))
}
