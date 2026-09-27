package celt

import "math"

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

// kissMulSubFast returns the kissMulSubSource product difference. It drops the
// exceptional flag, which is always false for the bounded inputs of the Fast
// butterflies.
func kissMulSubFast(a, b, c, d float32) float32 {
	v, _ := kissMulSubSource(a, b, c, d)
	return v
}

// kissMulAddFast returns the kissMulAddSource product sum without the
// exceptional flag, like kissMulSubFast.
func kissMulAddFast(a, b, c, d float32) float32 {
	v, _ := kissMulAddSource(a, b, c, d)
	return v
}

// kfBfly5InnerFast is kfBfly5InnerScalar for a stage whose FFT input passed
// kissFFTInputBounded: no twiddle product can be NaN, so the per-product
// non-finite handling is dropped. Every output keeps the scalar operation
// order.
func kfBfly5InnerFast(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	ya := w[fstride*m]
	yb := w[fstride*2*m]
	yar, yai := ya.r, ya.i
	ybr, ybi := yb.r, yb.i
	for i := 0; i < N; i++ {
		base := i * mm
		f0 := fout[base : base+m]
		f1 := fout[base+m : base+2*m]
		f2 := fout[base+2*m : base+3*m]
		f3 := fout[base+3*m : base+4*m]
		f4 := fout[base+4*m : base+5*m]
		f1 = f1[:len(f0)]
		f2 = f2[:len(f0)]
		f3 = f3[:len(f0)]
		f4 = f4[:len(f0)]
		tw := 0
		for u := range f0 {
			w1 := &w[tw]
			w2 := &w[2*tw]
			w3 := &w[3*tw]
			w4 := &w[4*tw]
			tw += fstride

			b1r, b1i := f1[u].r, f1[u].i
			b2r, b2i := f2[u].r, f2[u].i
			b3r, b3i := f3[u].r, f3[u].i
			b4r, b4i := f4[u].r, f4[u].i
			s1r := kissMulSubFast(b1r, w1.r, b1i, w1.i)
			s1i := kissMulAddFast(b1r, w1.i, b1i, w1.r)
			s2r := kissMulSubFast(b2r, w2.r, b2i, w2.i)
			s2i := kissMulAddFast(b2r, w2.i, b2i, w2.r)
			s3r := kissMulSubFast(b3r, w3.r, b3i, w3.i)
			s3i := kissMulAddFast(b3r, w3.i, b3i, w3.r)
			s4r := kissMulSubFast(b4r, w4.r, b4i, w4.i)
			s4i := kissMulAddFast(b4r, w4.i, b4i, w4.r)

			s7r, s7i := s1r+s4r, s1i+s4i
			s10r, s10i := s1r-s4r, s1i-s4i
			s8r, s8i := s2r+s3r, s2i+s3i
			s9r, s9i := s2r-s3r, s2i-s3i

			s0r, s0i := f0[u].r, f0[u].i
			f0[u].r = kissAdd(s0r, kissAdd(s7r, s8r))
			f0[u].i = kissAdd(s0i, kissAdd(s7i, s8i))

			s5r := kissAdd(s0r, kissMulAddFast(s7r, yar, s8r, ybr))
			s5i := kissAdd(s0i, kissMulAddFast(s7i, yar, s8i, ybr))
			s6r := kissMulAddFast(s10i, yai, s9i, ybi)
			s6i := -kissMulAddFast(s10r, yai, s9r, ybi)
			f1[u].r, f1[u].i = kissSub(s5r, s6r), kissSub(s5i, s6i)
			f4[u].r, f4[u].i = kissAdd(s5r, s6r), kissAdd(s5i, s6i)

			s11r := kissAdd(s0r, kissMulAddFast(s7r, ybr, s8r, yar))
			s11i := kissAdd(s0i, kissMulAddFast(s7i, ybr, s8i, yar))
			s12r := kissMulSubFast(s9i, yai, s10i, ybi)
			s12i := kissMulSubFast(s10r, ybi, s9r, yai)
			f2[u].r, f2[u].i = kissAdd(s11r, s12r), kissAdd(s11i, s12i)
			f3[u].r, f3[u].i = kissSub(s11r, s12r), kissSub(s11i, s12i)
		}
	}
}

// kfBfly3InnerFast is kfBfly3InnerScalar for bounded FFT input (see
// kfBfly5InnerFast).
func kfBfly3InnerFast(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	epi3i := w[fstride*m].i
	for i := 0; i < N; i++ {
		base := i * mm
		f0 := fout[base : base+m]
		f1 := fout[base+m : base+2*m]
		f2 := fout[base+2*m : base+3*m]
		f1 = f1[:len(f0)]
		f2 = f2[:len(f0)]
		tw := 0
		for j := range f0 {
			w1 := &w[tw]
			w2 := &w[2*tw]
			tw += fstride

			b1r, b1i := f1[j].r, f1[j].i
			b2r, b2i := f2[j].r, f2[j].i
			s1r := kissMulSubFast(b1r, w1.r, b1i, w1.i)
			s1i := kissMulAddFast(b1r, w1.i, b1i, w1.r)
			s2r := kissMulSubFast(b2r, w2.r, b2i, w2.i)
			s2i := kissMulAddFast(b2r, w2.i, b2i, w2.r)

			s3r := s1r + s2r
			s3i := s1i + s2i
			s0r := s1r - s2r
			s0i := s1i - s2i

			a0r, a0i := f0[j].r, f0[j].i
			h1r := kissHalfSub(a0r, s3r)
			h1i := kissHalfSub(a0i, s3i)
			s0r = kissScaleMul(s0r, epi3i)
			s0i = kissScaleMul(s0i, epi3i)

			f0[j].r, f0[j].i = a0r+s3r, a0i+s3i
			f2[j].r, f2[j].i = h1r+s0i, h1i-s0r
			f1[j].r, f1[j].i = h1r-s0i, h1i+s0r
		}
	}
}

// kfBfly4InnerFast is kfBfly4InnerScalar for bounded FFT input (see
// kfBfly5InnerFast).
func kfBfly4InnerFast(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	for i := 0; i < N; i++ {
		base := i * mm
		f0 := fout[base : base+m]
		f1 := fout[base+m : base+2*m]
		f2 := fout[base+2*m : base+3*m]
		f3 := fout[base+3*m : base+4*m]
		f1 = f1[:len(f0)]
		f2 = f2[:len(f0)]
		f3 = f3[:len(f0)]
		tw := 0
		for j := range f0 {
			w1 := &w[tw]
			w2 := &w[2*tw]
			w3 := &w[3*tw]
			tw += fstride

			b1r, b1i := f1[j].r, f1[j].i
			b2r, b2i := f2[j].r, f2[j].i
			b3r, b3i := f3[j].r, f3[j].i
			s0r := kissMulSubFast(b1r, w1.r, b1i, w1.i)
			s0i := kissMulAddFast(b1r, w1.i, b1i, w1.r)
			s1r := kissMulSubFast(b2r, w2.r, b2i, w2.i)
			s1i := kissMulAddFast(b2r, w2.i, b2i, w2.r)
			s2r := kissMulSubFast(b3r, w3.r, b3i, w3.i)
			s2i := kissMulAddFast(b3r, w3.i, b3i, w3.r)

			a0r, a0i := f0[j].r, f0[j].i
			s5r := a0r - s1r
			s5i := a0i - s1i
			f0r := a0r + s1r
			f0i := a0i + s1i
			s3r := s0r + s2r
			s3i := s0i + s2i
			s4r := s0r - s2r
			s4i := s0i - s2i
			f2[j].r, f2[j].i = f0r-s3r, f0i-s3i
			f0[j].r, f0[j].i = f0r+s3r, f0i+s3i
			f1[j].r, f1[j].i = s5r+s4i, s5i-s4r
			f3[j].r, f3[j].i = s5r-s4i, s5i+s4r
		}
	}
}
