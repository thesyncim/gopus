//go:build !arm64 && (!amd64 || nosimd || purego || !goexperiment.simd)

package celt

// imdctPreRotateFFT runs the clt_mdct_backward_c() pre-rotation and the
// forward FFT, returning the FFT output. Like libopus, the scalar build stores
// each pre-rotated value straight into its bit-reversed FFT slot, so fftIn is
// not used.
func imdctPreRotateFFT(fftIn []float32, fftTmp []float32, spectrum, trig []float32, n2, n4 int, st *kissFFTState) []float32 {
	if st == nil {
		st = getKissFFTState(n4)
	}
	if n4 <= 0 || n2 != 2*n4 || st == nil || len(st.bitrev) != n4 || len(fftTmp) < 2*n4 {
		imdctPreRotateF32Spectrum(fftIn, spectrum, trig, n2, n4)
		return kissFFT32InterleavedToScratch(fftIn[:2*n4], fftTmp, st)
	}
	fft := fftTmp[:2*n4]
	imdctPreRotateKissPairs(fft, st.bitrev, spectrum, trig, n4)
	st.fftImpl(fft)
	return fft
}

// imdctPreRotateFMA32Scalar matches the scalar AMD64 v3 float path in
// libopus celt/mdct.c clt_mdct_backward_c(). The C loop stores yi before yr,
// so the real component in fftIn is x1*t0 - round(x2*t1) and the imaginary
// component is x2*t0 + round(x1*t1).
func imdctPreRotateFMA32Scalar(fftIn []float32, spectrum []float32, trig []float32, n2, n4 int) {
	_ = fftIn[2*n4-1]
	for i := range n4 {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		fftIn[2*i] = fma32(x1, t0, -noFMA32Mul(x2, t1))
		fftIn[2*i+1] = fma32(x2, t0, noFMA32Mul(x1, t1))
	}
}

// imdctPreRotateFFTStrided is imdctPreRotateFFT for the spectrum
// coeffs[first], coeffs[first+stride], ... of n2 values, the interleaved short
// block that libopus clt_mdct_backward_c() reads with its stride argument.
// gather backs the contiguous copy the fallback needs.
func imdctPreRotateFFTStrided(fftIn []float32, fftTmp []float32, coeffs []float32, first, stride int, trig []float32, n2, n4 int, st *kissFFTState, gather []float32) []float32 {
	if st == nil {
		st = getKissFFTState(n4)
	}
	if st != nil && n4 > 0 && n2 == 2*n4 && len(st.bitrev) == n4 && len(fftTmp) >= 2*n4 {
		fft := fftTmp[:2*n4]
		imdctPreRotateKissStep(fft, st.bitrev, coeffs[first:first+(n2-1)*stride+1], stride, trig, n4)
		st.fftImpl(fft)
		return fft
	}
	gatherStrided(gather[:n2], coeffs, first, stride)
	return imdctPreRotateFFT(fftIn, fftTmp, gather[:n2], trig, n2, n4, st)
}

// imdctPreRotateKissPairs is the clt_mdct_backward_c() pre-rotation of a
// contiguous spectrum of n2 == 2*n4 values into the bit-reversed FFT slots.
func imdctPreRotateKissPairs(dst []float32, bitrev []int, spectrum, trig []float32, n4 int) {
	imdctPreRotateKissStep(dst, bitrev, spectrum[:2*n4], 1, trig, n4)
}

// imdctPreRotateKissStep is the clt_mdct_backward_c() pre-rotation for n2 ==
// 2*n4 spectrum values spaced stride apart in the checked span
// spectrum[:(n2-1)*stride+1]: x1 walks the even values up from the start and
// x2 the odd values down from the end, like libopus xp1 and xp2. Each rotated
// value is stored at dst[2*bitrev[i]..2*bitrev[i]+1].
func imdctPreRotateKissStep(dst []float32, bitrev []int, spectrum []float32, stride int, trig []float32, n4 int) {
	if n4 <= 0 {
		return
	}
	spectrum = spectrum[:(2*n4-1)*stride+1]
	dst = dst[:2*n4]
	t0s := trig[:n4]
	t1s := trig[n4 : 2*n4][:len(t0s)]
	bitrev = bitrev[:len(t0s)]
	step := 2 * stride
	off1 := 0
	off2 := (2*n4 - 1) * stride
	for i, r := range bitrev {
		x1 := spectrum[off1]
		x2 := spectrum[off2]
		off1 += step
		off2 -= step
		t0 := t0s[i]
		t1 := t1s[i]
		var yr, yi float32
		if mdctUseFMALikeMixEnabled {
			// libopus celt/mdct.c clt_mdct_backward_c() contracts the first
			// source product and rounds the second product before the add/sub.
			yr = fma32(x1, t0, -noFMA32Mul(x2, t1))
			yi = fma32(x2, t0, noFMA32Mul(x1, t1))
		} else {
			// The non-v3 targets round each product on its own.
			yr = float32(x1*t0) - float32(x2*t1)
			yi = float32(x2*t0) + float32(x1*t1)
		}
		dst[2*r] = yr
		dst[2*r+1] = yi
	}
}
