//go:build arm64 || (amd64 && goexperiment.simd && !nosimd && !purego)

package celt

// imdctPreRotateFFT runs the clt_mdct_backward_c() pre-rotation into fftIn,
// then bit-reverses it into fftTmp and runs the forward FFT, returning the FFT
// output.
func imdctPreRotateFFT(fftIn []float32, fftTmp []float32, spectrum, trig []float32, n2, n4 int, st *kissFFTState) []float32 {
	imdctPreRotateF32Spectrum(fftIn, spectrum, trig, n2, n4)
	return kissFFT32InterleavedToScratch(fftIn[:2*n4], fftTmp, st)
}

// imdctPreRotateFFTStrided gathers the spectrum coeffs[first],
// coeffs[first+stride], ... of n2 values into gather and runs imdctPreRotateFFT
// on it.
func imdctPreRotateFFTStrided(fftIn []float32, fftTmp []float32, coeffs []float32, first, stride int, trig []float32, n2, n4 int, st *kissFFTState, gather []float32) []float32 {
	gatherStrided(gather[:n2], coeffs, first, stride)
	return imdctPreRotateFFT(fftIn, fftTmp, gather[:n2], trig, n2, n4, st)
}
