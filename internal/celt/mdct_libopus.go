package celt

import (
	"math"
	"sync"
)

// This file contains a libopus-style IMDCT implementation.
// Based on libopus celt/mdct.c clt_mdct_backward_c structure.

var (
	libopusTrigMu    sync.Mutex
	libopusTrigCache = map[int][]float64{}
)

// getLibopusTrig returns the trig table matching libopus's format.
// trig[i] = cos(2*pi*(i+0.125)/N) for i=0..N/2-1
func getLibopusTrig(n int) []float64 {
	libopusTrigMu.Lock()
	defer libopusTrigMu.Unlock()

	if trig, ok := libopusTrigCache[n]; ok {
		return trig
	}

	n2 := n / 2
	trig := make([]float64, n2)
	for i := 0; i < n2; i++ {
		trig[i] = math.Cos(2.0 * math.Pi * (float64(i) + 0.125) / float64(n))
	}

	libopusTrigCache[n] = trig
	return trig
}

// libopusIMDCT implements IMDCT following libopus clt_mdct_backward_c structure.
// Input: spectrum of length N (e.g., 960)
// Output: N samples (windowed IMDCT output)
// prevOverlap: previous frame's overlap buffer (length = overlap)
// overlap: overlap size (e.g., 120)
//
// Returns: frame samples (length N) + new overlap (length overlap)
func libopusIMDCT(spectrum []float64, prevOverlap []float64, overlap int) []float64 {
	n2 := len(spectrum) // N2 = frame size = 960
	if n2 == 0 {
		return nil
	}

	n := n2 * 2  // N = MDCT size = 1920
	n4 := n2 / 2 // N4 = FFT size = 480

	// Output buffer: N2 + overlap = 960 + 120 = 1080
	out := make([]float64, n2+overlap)

	// Copy prevOverlap to out[0:overlap] for TDAC
	if overlap > 0 && len(prevOverlap) > 0 {
		copyLen := overlap
		if len(prevOverlap) < copyLen {
			copyLen = len(prevOverlap)
		}
		copy(out[:copyLen], prevOverlap[:copyLen])
	}

	trig := getLibopusTrig(n)

	// Pre-rotate: convert spectrum to complex FFT input
	// Following libopus structure
	fftIn := make([]complex128, n4)
	for i := 0; i < n4; i++ {
		// Input indices (matching libopus)
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]

		t0 := trig[i]
		t1 := trig[n4+i]

		yr := x2*t0 + x1*t1
		yi := x1*t0 - x2*t1

		// Store as complex (swapped: yi is real, yr is imag) - matching libopus
		fftIn[i] = complex(yi, yr)
	}

	// DFT (works for any size, including 480 which is not power of 2)
	fftOut := dft(fftIn)

	// Convert back to interleaved format in out buffer
	for i := 0; i < n4; i++ {
		v := fftOut[i]
		out[overlap/2+2*i] = real(v)
		out[overlap/2+2*i+1] = imag(v)
	}

	// Post-rotate and de-shuffle
	yp0 := overlap / 2
	yp1 := overlap/2 + n2 - 2

	for i := 0; i < (n4+1)>>1; i++ {
		re := out[yp0+1]
		im := out[yp0]
		t0 := trig[i]
		t1 := trig[n4+i]

		yr := re*t0 + im*t1
		yi := re*t1 - im*t0

		re2 := out[yp1+1]
		im2 := out[yp1]

		out[yp0] = yr
		out[yp1+1] = yi

		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]

		yr = re2*t0 + im2*t1
		yi = re2*t1 - im2*t0

		out[yp1] = yr
		out[yp0+1] = yi

		yp0 += 2
		yp1 -= 2
	}

	// TDAC windowing: mirror on both sides
	if overlap > 0 {
		window := GetWindowBuffer(overlap)
		xp1 := overlap - 1
		yp1Idx := 0
		wp1 := 0
		wp2 := overlap - 1

		for i := 0; i < overlap/2; i++ {
			x1 := out[xp1]
			x2 := out[yp1Idx]
			out[yp1Idx] = x2*window[wp2] - x1*window[wp1]
			out[xp1] = x2*window[wp1] + x1*window[wp2]
			yp1Idx++
			xp1--
			wp1++
			wp2--
		}
	}

	return out
}
