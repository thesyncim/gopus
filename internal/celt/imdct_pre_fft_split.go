//go:build arm64 || (amd64 && goexperiment.simd && !nosimd && !purego)

package celt

// imdctPreRotateFFT runs the clt_mdct_backward_c() pre-rotation into fftIn,
// then bit-reverses it into fftTmp and runs the forward FFT, returning the FFT
// output.
func imdctPreRotateFFT(fftIn []complex64, fftTmp []kissCpx, spectrum, trig []float32, n2, n4 int, st *kissFFTState) []kissCpx {
	imdctPreRotateF32Spectrum(fftIn, spectrum, trig, n2, n4)
	return kissFFT32ToScratch(fftIn, fftTmp, st)
}
