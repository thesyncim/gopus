package celt

import (
	"math"
	"unsafe"
)

// kissFFTFastLimitBits is 2^100 as float32 bits. When every input component of
// an FFT is below it in magnitude, no stage can overflow: each radix-3/4/5
// stage grows the component magnitude by less than 20x and twiddles are at most
// 1, so all products and sums stay finite and no twiddle product is NaN.
const kissFFTFastLimitBits = 0x71800000

// kissFFTInputBounded reports whether every component of x is finite and below
// 2^100 in magnitude. NaN and infinity fail the test.
func kissFFTInputBounded(x []kissCpx) bool {
	// Adding 2^31-limit to the magnitude bits carries into bit 31 exactly when
	// the magnitude reaches the limit.
	const bias = 0x80000000 - kissFFTFastLimitBits
	var accR, accI uint32
	for _, v := range x {
		accR |= (math.Float32bits(v.r) & 0x7fffffff) + bias
		accI |= (math.Float32bits(v.i) & 0x7fffffff) + bias
	}
	return (accR|accI)>>31 == 0
}

// kissStageTwiddles holds one FFT stage's twiddles packed per butterfly: for
// butterfly u of a radix-p stage with stride fstride, entry u holds
// w[k*u*fstride] for k = 1..p-1. Only the slice matching the stage radix is
// set, and only for stages with m > 1. Radix-4 entries carry an unused fourth
// slot so every entry size is a power of two and indexes with a shift.
type kissStageTwiddles struct {
	tw3 [][2]kissCpx
	tw4 [][4]kissCpx
	tw5 [][4]kissCpx
}

// newKissStageTwiddles packs the per-stage twiddles fftImpl passes to the Fast
// butterflies.
func newKissStageTwiddles(factors []int, fstride []int, shift int, w []kissCpx) []kissStageTwiddles {
	shift = max(shift, 0)
	stages := make([]kissStageTwiddles, len(factors)/2)
	for i := range stages {
		p, m := factors[2*i], factors[2*i+1]
		if m <= 1 || i >= len(fstride) {
			continue
		}
		fs := fstride[i] << shift
		switch p {
		case 3:
			stages[i].tw3 = packKissTwiddles3(w, m, fs)
		case 4:
			stages[i].tw4 = packKissTwiddles4(w, m, fs)
		case 5:
			stages[i].tw5 = packKissTwiddles5(w, m, fs)
		}
	}
	return stages
}

func packKissTwiddles3(w []kissCpx, m, fstride int) [][2]kissCpx {
	tw := make([][2]kissCpx, m)
	for u := range tw {
		tw[u] = [2]kissCpx{w[u*fstride], w[2*u*fstride]}
	}
	return tw
}

func packKissTwiddles4(w []kissCpx, m, fstride int) [][4]kissCpx {
	tw := make([][4]kissCpx, m)
	for u := range tw {
		tw[u] = [4]kissCpx{w[u*fstride], w[2*u*fstride], w[3*u*fstride]}
	}
	return tw
}

func packKissTwiddles5(w []kissCpx, m, fstride int) [][4]kissCpx {
	tw := make([][4]kissCpx, m)
	for u := range tw {
		tw[u] = [4]kissCpx{w[u*fstride], w[2*u*fstride], w[3*u*fstride], w[4*u*fstride]}
	}
	return tw
}

// kissFloats views x as its interleaved float32 components, so the Fast
// butterflies address both halves of element u as [2u] and [2u+1] with a
// scaled index instead of recomputing each element address.
func kissFloats(x []kissCpx) []float32 {
	if len(x) == 0 {
		return nil
	}
	return unsafe.Slice(&x[0].r, 2*len(x))
}

// kfBfly5InnerFast is kfBfly5InnerScalar for a stage whose FFT input passed
// kissFFTInputBounded, with the stage twiddles packed per butterfly and
// ya = w[fstride*m], yb = w[2*fstride*m]. No product can be NaN, so the
// per-product non-finite handling is dropped, and every output keeps the
// scalar operation order.
func kfBfly5InnerFast(fout []kissCpx, tw [][4]kissCpx, ya, yb kissCpx, N, mm int) {
	m := len(tw)
	// y lives in memory so the loop reads its components as multiply
	// operands instead of pinning four registers.
	y := [4]float32{ya.r, ya.i, yb.r, yb.i}
	for i := 0; i < N; i++ {
		f := kissFloats(fout[i*mm : i*mm+5*m])
		f0 := f[:2*m]
		f1 := f[2*m : 4*m]
		f2 := f[4*m : 6*m]
		f3 := f[6*m : 8*m]
		f4 := f[8*m : 10*m]
		f1 = f1[:len(f0)]
		f2 = f2[:len(f0)]
		f3 = f3[:len(f0)]
		f4 = f4[:len(f0)]
		for r := 0; r+1 < len(f0); r += 2 {
			t := &tw[uint(r)/2]
			q := r + 1
			s1r := kissMulSubFast(f1[r], t[0].r, f1[q], t[0].i)
			s1i := kissMulAddFast(f1[r], t[0].i, f1[q], t[0].r)
			s4r := kissMulSubFast(f4[r], t[3].r, f4[q], t[3].i)
			s4i := kissMulAddFast(f4[r], t[3].i, f4[q], t[3].r)
			s7r, s7i := s1r+s4r, s1i+s4i
			s10r, s10i := s1r-s4r, s1i-s4i
			s2r := kissMulSubFast(f2[r], t[1].r, f2[q], t[1].i)
			s2i := kissMulAddFast(f2[r], t[1].i, f2[q], t[1].r)
			s3r := kissMulSubFast(f3[r], t[2].r, f3[q], t[2].i)
			s3i := kissMulAddFast(f3[r], t[2].i, f3[q], t[2].r)
			s8r, s8i := s2r+s3r, s2i+s3i
			s9r, s9i := s2r-s3r, s2i-s3i

			// Real outputs, then imaginary outputs. s6i holds the negation
			// of the scalar kernel's scratch[6].i, so its sign moves into
			// the two sums that use it.
			s0r := f0[r]
			f0[r] = s0r + (s7r + s8r)
			s5r := s0r + kissMulAddFast(s7r, y[0], s8r, y[2])
			s6r := kissMulAddFast(s10i, y[1], s9i, y[3])
			f1[r], f4[r] = s5r-s6r, s5r+s6r
			s11r := s0r + kissMulAddFast(s7r, y[2], s8r, y[0])
			s12r := kissMulSubFast(s9i, y[1], s10i, y[3])
			f2[r], f3[r] = s11r+s12r, s11r-s12r

			s0i := f0[q]
			f0[q] = s0i + (s7i + s8i)
			s5i := s0i + kissMulAddFast(s7i, y[0], s8i, y[2])
			s6i := kissMulAddFast(s10r, y[1], s9r, y[3])
			f1[q], f4[q] = s5i+s6i, s5i-s6i
			s11i := s0i + kissMulAddFast(s7i, y[2], s8i, y[0])
			s12i := kissMulSubFast(s10r, y[3], s9r, y[1])
			f2[q], f3[q] = s11i+s12i, s11i-s12i
		}
	}
}

// kfBfly3InnerFast is kfBfly3InnerScalar for bounded FFT input with packed
// stage twiddles and epi3i = w[fstride*m].i (see kfBfly5InnerFast).
func kfBfly3InnerFast(fout []kissCpx, tw [][2]kissCpx, epi3i float32, N, mm int) {
	m := len(tw)
	for i := 0; i < N; i++ {
		f := kissFloats(fout[i*mm : i*mm+3*m])
		f0 := f[:2*m]
		f1 := f[2*m : 4*m]
		f2 := f[4*m : 6*m]
		f1 = f1[:len(f0)]
		f2 = f2[:len(f0)]
		for r := 0; r+1 < len(f0); r += 2 {
			t := &tw[uint(r)/2]
			q := r + 1
			s1r := kissMulSubFast(f1[r], t[0].r, f1[q], t[0].i)
			s1i := kissMulAddFast(f1[r], t[0].i, f1[q], t[0].r)
			s2r := kissMulSubFast(f2[r], t[1].r, f2[q], t[1].i)
			s2i := kissMulAddFast(f2[r], t[1].i, f2[q], t[1].r)

			s3r := s1r + s2r
			s3i := s1i + s2i
			s0r := kissScaleMul(s1r-s2r, epi3i)
			s0i := kissScaleMul(s1i-s2i, epi3i)

			a0r, a0i := f0[r], f0[q]
			h1r := kissHalfSub(a0r, s3r)
			h1i := kissHalfSub(a0i, s3i)
			f0[r], f0[q] = a0r+s3r, a0i+s3i
			f2[r], f2[q] = h1r+s0i, h1i-s0r
			f1[r], f1[q] = h1r-s0i, h1i+s0r
		}
	}
}

// kfBfly4InnerFast is kfBfly4InnerScalar for bounded FFT input with packed
// stage twiddles (see kfBfly5InnerFast).
func kfBfly4InnerFast(fout []kissCpx, tw [][4]kissCpx, N, mm int) {
	m := len(tw)
	for i := 0; i < N; i++ {
		f := kissFloats(fout[i*mm : i*mm+4*m])
		f0 := f[:2*m]
		f1 := f[2*m : 4*m]
		f2 := f[4*m : 6*m]
		f3 := f[6*m : 8*m]
		f1 = f1[:len(f0)]
		f2 = f2[:len(f0)]
		f3 = f3[:len(f0)]
		for r := 0; r+1 < len(f0); r += 2 {
			t := &tw[uint(r)/2]
			q := r + 1
			s0r := kissMulSubFast(f1[r], t[0].r, f1[q], t[0].i)
			s0i := kissMulAddFast(f1[r], t[0].i, f1[q], t[0].r)
			s1r := kissMulSubFast(f2[r], t[1].r, f2[q], t[1].i)
			s1i := kissMulAddFast(f2[r], t[1].i, f2[q], t[1].r)
			s2r := kissMulSubFast(f3[r], t[2].r, f3[q], t[2].i)
			s2i := kissMulAddFast(f3[r], t[2].i, f3[q], t[2].r)

			a0r, a0i := f0[r], f0[q]
			s5r := a0r - s1r
			s5i := a0i - s1i
			f0r := a0r + s1r
			f0i := a0i + s1i
			s3r := s0r + s2r
			s3i := s0i + s2i
			s4r := s0r - s2r
			s4i := s0i - s2i
			f2[r], f2[q] = f0r-s3r, f0i-s3i
			f0[r], f0[q] = f0r+s3r, f0i+s3i
			f1[r], f1[q] = s5r+s4i, s5i-s4r
			f3[r], f3[q] = s5r-s4i, s5i+s4r
		}
	}
}
