//go:build !arm64 && (!amd64 || nosimd || !goexperiment.simd)

package celt

// imdctPreRotateFFT runs the clt_mdct_backward_c() pre-rotation and the
// forward FFT, returning the FFT output. Like libopus, the scalar build stores
// each pre-rotated value straight into its bit-reversed FFT slot, so fftIn is
// not used.
func imdctPreRotateFFT(fftIn []complex64, fftTmp []kissCpx, spectrum, trig []float32, n2, n4 int, st *kissFFTState) []kissCpx {
	if st == nil {
		st = getKissFFTState(n4)
	}
	if n4 <= 0 || st == nil || len(st.bitrev) != n4 || len(fftTmp) < n4 {
		imdctPreRotateF32Spectrum(fftIn, spectrum, trig, n2, n4)
		return kissFFT32ToScratch(fftIn, fftTmp, st)
	}
	fft := fftTmp[:n4]
	imdctPreRotateKissScalar(fft, st.bitrev, spectrum, trig, n2, n4)
	st.fftImpl(fft)
	return fft
}

// imdctPreRotateKissScalar is imdctPreRotateNoFMAScalar with the result
// written to dst[bitrev[i]] instead of fftIn[i].
func imdctPreRotateKissScalar(dst []kissCpx, bitrev []int, spectrum, trig []float32, n2, n4 int) {
	spectrum = spectrum[:n2]
	t0s := trig[:n4]
	t1s := trig[n4 : 2*n4]
	bitrev = bitrev[:len(t0s)]
	t1s = t1s[:len(t0s)]
	for i, rev := range bitrev {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := t0s[i]
		t1 := t1s[i]
		// The conversions round each product on its own, as noFMA32Mul does.
		dst[rev] = kissCpx{
			float32(x1*t0) - float32(x2*t1),
			float32(x2*t0) + float32(x1*t1),
		}
	}
}
