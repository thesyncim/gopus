//go:build !arm64 && !amd64

package celt

import "unsafe"

// imdctPreRotateF32 is the pure-Go hot path used by float32 IMDCT decode.
// It writes directly into fftIn backing storage as interleaved float32 (re, im).
func imdctPreRotateF32(fftIn []complex64, spectrum []float64, trig []float32, n2, n4 int) {
	if n4 <= 0 {
		return
	}

	_ = spectrum[n2-1] // BCE hints.
	_ = trig[n2-1]
	out := unsafe.Slice((*float32)(unsafe.Pointer(&fftIn[0])), n4*2)
	_ = out[n4*2-1]

	i := 0
	for ; i+1 < n4; i += 2 {
		x10 := float32(spectrum[2*i])
		x20 := float32(spectrum[n2-1-2*i])
		t00 := trig[i]
		t10 := trig[n4+i]
		b0 := 2 * i
		out[b0] = x10*t00 - x20*t10
		out[b0+1] = x20*t00 + x10*t10

		i1 := i + 1
		x11 := float32(spectrum[2*i1])
		x21 := float32(spectrum[n2-1-2*i1])
		t01 := trig[i1]
		t11 := trig[n4+i1]
		b1 := 2 * i1
		out[b1] = x11*t01 - x21*t11
		out[b1+1] = x21*t01 + x11*t11
	}

	if i < n4 {
		x1 := float32(spectrum[2*i])
		x2 := float32(spectrum[n2-1-2*i])
		t0 := trig[i]
		t1 := trig[n4+i]
		b := 2 * i
		out[b] = x1*t0 - x2*t1
		out[b+1] = x2*t0 + x1*t1
	}
}

// imdctPostRotateF32 is the pure-Go hot path used by float32 IMDCT decode.
func imdctPostRotateF32(buf []float32, trig []float32, n2, n4 int) {
	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}

	_ = buf[n2-1] // BCE hints.
	_ = trig[n2-1]

	yp0 := 0
	yp1 := n2 - 2
	for i := 0; i < limit; i++ {
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
}
