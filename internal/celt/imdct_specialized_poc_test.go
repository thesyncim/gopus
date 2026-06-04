package celt

import (
	"math"
	"testing"
)

// imdct_specialized_poc_test.go MEASURES the headroom for a specialized/unrolled
// IMDCT against the production transform. It is a measurement scaffold only: it
// times the exact transform sizes the CELT 48 kHz 20 ms mono decode bench drives
// (frame N=960 -> n2=480 spectrum -> n4=240-point complex FFT) so the FFT, the
// pre/post rotation, and the whole IMDCT can be timed in isolation under both the
// default (NEON/amd64 asm) build and the `-tags purego` (scalar Go) build.
//
// The point of the POC: on the asm tier the FFT butterflies + IMDCT rotations are
// already hand-written NEON (kf_bfly_arm64.s, imdct_pre/post_kiss_arm64.s). A
// pure-Go unrolled transform would have to BEAT that NEON. Running these
// benchmarks under `-tags purego` (scalar Go, the best a pure-Go unrolled
// transform can do without SIMD codegen) vs the default build (NEON) quantifies
// the gap directly — no new transform code, no bit-exactness risk.

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
// transform size (n4=240). Under the default build this is the NEON butterfly
// path; under `-tags purego` it is the scalar-Go fallback. Compare the two.
func BenchmarkPOCFFT240(b *testing.B) {
	in := pocFFTInput240()
	scratch := make([]kissCpx, len(in))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = kissFFT32ToScratch(in, scratch)
	}
}

// BenchmarkPOCIMDCT480 times the full IMDCT used by the 48 kHz 20 ms mono decode
// (pre-rotate + FFT + post-rotate + TDAC), matching synthesizeMonoLongToFloat32:
// spectrum n2=480, overlap=120. Under the default build the rotations + FFT are
// NEON asm; under `-tags purego` they are scalar Go.
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
