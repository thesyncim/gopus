package celt

import (
	"math"
	"testing"
)

// imdct_specialized_poc_test.go measures headroom for a specialized/unrolled
// IMDCT against the production transform. It times the exact transform sizes
// used by the CELT 48 kHz 20 ms mono decode benchmark (frame N=960 -> n2=480
// spectrum -> n4=240-point complex FFT), so the FFT, pre/post rotation, and whole
// IMDCT can be measured in isolation. Run with GOEXPERIMENT=simd to measure the
// opt-in Go SIMD kernels, and with the ordinary build or `-tags nosimd` to
// measure scalar Go. The ordinary arm64 build is scalar.

// pocFFTInput240 builds a deterministic n=240 complex input (pre-rotated spectrum
// shape) for the FFT-only benchmark.
func pocFFTInput240() []complex64 {
	const n = 240
	in := make([]complex64, n)
	for i := range in {
		r := float32(math.Sin(float64(i)*0.079)*0.9 + math.Cos(float64(i+3)*0.031)*0.2)
		im := float32(math.Cos(float64(i)*0.053)*0.7 - math.Sin(float64(i+11)*0.017)*0.3)
		in[i] = complex(r, im)
	}
	return in
}

// BenchmarkPOCFFT240 times the production complex FFT at the decode bench's
// transform size (n4=240). Its implementation follows GOARCH and build
// constraints; GOEXPERIMENT=simd enables eligible Go SIMD kernels.
func BenchmarkPOCFFT240(b *testing.B) {
	in := pocFFTInput240()
	scratch := make([]kissCpx, len(in))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = kissFFT32ToScratch(in, scratch, nil)
	}
}

// BenchmarkPOCIMDCT480 times the full IMDCT used by the 48 kHz 20 ms mono decode
// (pre-rotate + FFT + post-rotate + TDAC), matching synthesizeMonoLongToFloat32:
// spectrum n2=480, overlap=120. Run with GOEXPERIMENT=simd to measure eligible
// Go SIMD kernels, or with the ordinary/nosimd build to measure scalar Go.
func BenchmarkPOCIMDCT480(b *testing.B) {
	const (
		n2      = 480
		overlap = 120
	)
	spectrum := make([]float32, n2)
	for i := range spectrum {
		spectrum[i] = float32(math.Sin(float64(i)*0.017)*0.8 + math.Cos(float64(i+5)*0.013)*0.3)
	}
	prev := make([]float32, overlap)
	var scratch imdctScratchF32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = imdctOverlapWithPrevScratchF32Output32(spectrum, prev, overlap, &scratch)
	}
}
