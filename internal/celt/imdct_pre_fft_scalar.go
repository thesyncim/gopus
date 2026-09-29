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
	imdctPreRotateKissScalar(fft, st.bitrevFloat, spectrum, trig, n2, n4)
	st.fftImpl(fft)
	return fft
}

// imdctPreRotateKissScalar applies the selected scalar pre-rotation with its
// result written to dst[bitrev[i]] instead of fftIn[i]; revFloat holds the
// float offsets 2*bitrev[i] (kissFFTState.bitrevFloat).
func imdctPreRotateKissScalar(dst []kissCpx, revFloat []int, spectrum, trig []float32, n2, n4 int) {
	out := kissFloats(dst)
	spectrum = spectrum[:n2]
	t0s := trig[:n4]
	t1s := trig[n4 : 2*n4]
	revFloat = revFloat[:len(t0s)]
	t1s = t1s[:len(t0s)]
	// x1 walks the even spectrum entries up from the start and x2 the odd
	// entries down from the end, like libopus xp1 and xp2.
	j1, j2 := 0, n2-1
	for i, o := range revFloat {
		x1 := spectrum[j1]
		x2 := spectrum[j2]
		j1 += 2
		j2 -= 2
		t0 := t0s[i]
		t1 := t1s[i]
		if mdctUseFMALikeMixEnabled {
			// libopus celt/mdct.c clt_mdct_backward_c() contracts the first
			// source product and rounds the second product before the add/sub.
			out[o] = fma32(x1, t0, -noFMA32Mul(x2, t1))
			out[o+1] = fma32(x2, t0, noFMA32Mul(x1, t1))
		} else {
			// The non-v3 targets round each product on its own.
			out[o] = float32(x1*t0) - float32(x2*t1)
			out[o+1] = float32(x2*t0) + float32(x1*t1)
		}
	}
}

// imdctPreRotateFMA32Scalar matches the scalar AMD64 v3 float path in
// libopus celt/mdct.c clt_mdct_backward_c(). The C loop stores yi before yr,
// so the real component in fftIn is x1*t0 - round(x2*t1) and the imaginary
// component is x2*t0 + round(x1*t1).
func imdctPreRotateFMA32Scalar(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	for i := range n4 {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		fftIn[i] = complex(
			fma32(x1, t0, -noFMA32Mul(x2, t1)),
			fma32(x2, t0, noFMA32Mul(x1, t1)),
		)
	}
}
