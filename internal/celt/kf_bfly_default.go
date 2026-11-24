package celt

// kfBfly5InnerScalar is the scalar radix-5 butterfly inner loop.
func kfBfly5InnerScalar(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	ya := w[fstride*m]
	yb := w[fstride*2*m]
	yar, yai := ya.r, ya.i
	ybr, ybi := yb.r, yb.i
	_ = fout[2*N*mm-1]
	fstride2 := fstride * 2
	fstride3 := fstride * 3
	fstride4 := fstride * 4
	if m > 0 {
		_ = w[(m-1)*fstride4]
	}
	m2 := 2 * m
	mm2 := 2 * mm
	for i := 0; i < N; i++ {
		base := i * mm2
		idx0, idx1, idx2, idx3, idx4 := base, base+m2, base+2*m2, base+3*m2, base+4*m2
		tw1, tw2, tw3, tw4 := 0, 0, 0, 0
		for u := 0; u < m; u++ {
			s0r, s0i := fout[idx0], fout[idx0+1]
			b1r, b1i := fout[idx1], fout[idx1+1]
			b2r, b2i := fout[idx2], fout[idx2+1]
			b3r, b3i := fout[idx3], fout[idx3+1]
			b4r, b4i := fout[idx4], fout[idx4+1]
			w1 := w[tw1]
			w2 := w[tw2]
			w3 := w[tw3]
			w4 := w[tw4]

			s1r, exceptional := kissMulSubSource(b1r, w1.r, b1i, w1.i)
			if exceptional {
				s1r = kissMulSubSourceNonFinite(b1r, w1.r, b1i, w1.i)
			}
			s1i, exceptional := kissMulAddSource(b1r, w1.i, b1i, w1.r)
			if exceptional {
				s1i = kissMulAddSourceNonFinite(b1r, w1.i, b1i, w1.r)
			}
			s2r, exceptional := kissMulSubSource(b2r, w2.r, b2i, w2.i)
			if exceptional {
				s2r = kissMulSubSourceNonFinite(b2r, w2.r, b2i, w2.i)
			}
			s2i, exceptional := kissMulAddSource(b2r, w2.i, b2i, w2.r)
			if exceptional {
				s2i = kissMulAddSourceNonFinite(b2r, w2.i, b2i, w2.r)
			}
			s3r, exceptional := kissMulSubSource(b3r, w3.r, b3i, w3.i)
			if exceptional {
				s3r = kissMulSubSourceNonFinite(b3r, w3.r, b3i, w3.i)
			}
			s3i, exceptional := kissMulAddSource(b3r, w3.i, b3i, w3.r)
			if exceptional {
				s3i = kissMulAddSourceNonFinite(b3r, w3.i, b3i, w3.r)
			}
			s4r, exceptional := kissMulSubSource(b4r, w4.r, b4i, w4.i)
			if exceptional {
				s4r = kissMulSubSourceNonFinite(b4r, w4.r, b4i, w4.i)
			}
			s4i, exceptional := kissMulAddSource(b4r, w4.i, b4i, w4.r)
			if exceptional {
				s4i = kissMulAddSourceNonFinite(b4r, w4.i, b4i, w4.r)
			}

			s7r, s7i := s1r+s4r, s1i+s4i
			s10r, s10i := s1r-s4r, s1i-s4i
			s8r, s8i := s2r+s3r, s2i+s3i
			s9r, s9i := s2r-s3r, s2i-s3i

			fout[idx0] = kissAdd(s0r, kissAdd(s7r, s8r))
			fout[idx0+1] = kissAdd(s0i, kissAdd(s7i, s8i))

			s5rAdd, exceptional := kissMulAddSource(s7r, yar, s8r, ybr)
			if exceptional {
				s5rAdd = kissMulAddSourceNonFinite(s7r, yar, s8r, ybr)
			}
			s5r := kissAdd(s0r, s5rAdd)
			s5iAdd, exceptional := kissMulAddSource(s7i, yar, s8i, ybr)
			if exceptional {
				s5iAdd = kissMulAddSourceNonFinite(s7i, yar, s8i, ybr)
			}
			s5i := kissAdd(s0i, s5iAdd)
			s6r, exceptional := kissMulAddSource(s10i, yai, s9i, ybi)
			if exceptional {
				s6r = kissMulAddSourceNonFinite(s10i, yai, s9i, ybi)
			}
			s6iMul, exceptional := kissMulAddSource(s10r, yai, s9r, ybi)
			if exceptional {
				s6iMul = kissMulAddSourceNonFinite(s10r, yai, s9r, ybi)
			}
			s6i := -s6iMul
			fout[idx1], fout[idx1+1] = kissSub(s5r, s6r), kissSub(s5i, s6i)
			fout[idx4], fout[idx4+1] = kissAdd(s5r, s6r), kissAdd(s5i, s6i)

			s11rAdd, exceptional := kissMulAddSource(s7r, ybr, s8r, yar)
			if exceptional {
				s11rAdd = kissMulAddSourceNonFinite(s7r, ybr, s8r, yar)
			}
			s11r := kissAdd(s0r, s11rAdd)
			s11iAdd, exceptional := kissMulAddSource(s7i, ybr, s8i, yar)
			if exceptional {
				s11iAdd = kissMulAddSourceNonFinite(s7i, ybr, s8i, yar)
			}
			s11i := kissAdd(s0i, s11iAdd)
			s12r, exceptional := kissMulSubSource(s9i, yai, s10i, ybi)
			if exceptional {
				s12r = kissMulSubSourceNonFinite(s9i, yai, s10i, ybi)
			}
			s12i, exceptional := kissMulSubSource(s10r, ybi, s9r, yai)
			if exceptional {
				s12i = kissMulSubSourceNonFinite(s10r, ybi, s9r, yai)
			}
			fout[idx2], fout[idx2+1] = kissAdd(s11r, s12r), kissAdd(s11i, s12i)
			fout[idx3], fout[idx3+1] = kissSub(s11r, s12r), kissSub(s11i, s12i)

			idx0 += 2
			idx1 += 2
			idx2 += 2
			idx3 += 2
			idx4 += 2
			tw1 += fstride
			tw2 += fstride2
			tw3 += fstride3
			tw4 += fstride4
		}
	}
}

// kfBfly3InnerScalar is the scalar radix-3 butterfly inner loop.
func kfBfly3InnerScalar(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	m2 := 2 * m
	m4 := 4 * m
	mm2 := 2 * mm
	epi3i := w[fstride*m].i
	_ = fout[2*N*mm-1]
	fstride2 := fstride * 2
	if m > 0 {
		_ = w[(m-1)*fstride2]
	}
	for i := 0; i < N; i++ {
		base := i * mm2
		tw1, tw2 := 0, 0
		for j := 0; j < m; j++ {
			idx0 := base + 2*j
			idx1 := idx0 + m2
			idx2 := idx0 + m4

			a0r, a0i := fout[idx0], fout[idx0+1]
			b1r, b1i := fout[idx1], fout[idx1+1]
			b2r, b2i := fout[idx2], fout[idx2+1]
			w1 := w[tw1]
			w2 := w[tw2]

			s1r, exceptional := kissMulSubSource(b1r, w1.r, b1i, w1.i)
			if exceptional {
				s1r = kissMulSubSourceNonFinite(b1r, w1.r, b1i, w1.i)
			}
			s1i, exceptional := kissMulAddSource(b1r, w1.i, b1i, w1.r)
			if exceptional {
				s1i = kissMulAddSourceNonFinite(b1r, w1.i, b1i, w1.r)
			}
			s2r, exceptional := kissMulSubSource(b2r, w2.r, b2i, w2.i)
			if exceptional {
				s2r = kissMulSubSourceNonFinite(b2r, w2.r, b2i, w2.i)
			}
			s2i, exceptional := kissMulAddSource(b2r, w2.i, b2i, w2.r)
			if exceptional {
				s2i = kissMulAddSourceNonFinite(b2r, w2.i, b2i, w2.r)
			}

			s3r := s1r + s2r
			s3i := s1i + s2i
			s0r := s1r - s2r
			s0i := s1i - s2i

			tw1 += fstride
			tw2 += fstride2

			f1r := kissHalfSub(a0r, s3r)
			f1i := kissHalfSub(a0i, s3i)
			fout[idx0] = a0r + s3r
			fout[idx0+1] = a0i + s3i
			fout[idx1], fout[idx1+1], fout[idx2], fout[idx2+1] = kissRadix3ScaledOutputs(f1r, f1i, s0r, s0i, epi3i)
		}
	}
}

// kfBfly4InnerScalar is the scalar radix-4 butterfly inner loop.
func kfBfly4InnerScalar(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	m2 := 2 * m
	m4 := 4 * m
	m6 := 6 * m
	mm2 := 2 * mm
	_ = fout[2*N*mm-1]
	fstride2 := fstride * 2
	fstride3 := fstride * 3
	if m > 0 {
		_ = w[(m-1)*fstride3]
	}
	for i := 0; i < N; i++ {
		base := i * mm2
		tw1, tw2, tw3 := 0, 0, 0
		for j := 0; j < m; j++ {
			idx0 := base + 2*j
			idx1 := idx0 + m2
			idx2 := idx0 + m4
			idx3 := idx0 + m6

			f0r, f0i := fout[idx0], fout[idx0+1]
			b1r, b1i := fout[idx1], fout[idx1+1]
			b2r, b2i := fout[idx2], fout[idx2+1]
			b3r, b3i := fout[idx3], fout[idx3+1]
			w1 := w[tw1]
			w2 := w[tw2]
			w3 := w[tw3]

			s0r, exceptional := kissMulSubSource(b1r, w1.r, b1i, w1.i)
			if exceptional {
				s0r = kissMulSubSourceNonFinite(b1r, w1.r, b1i, w1.i)
			}
			s0i, exceptional := kissMulAddSource(b1r, w1.i, b1i, w1.r)
			if exceptional {
				s0i = kissMulAddSourceNonFinite(b1r, w1.i, b1i, w1.r)
			}
			s1r, exceptional := kissMulSubSource(b2r, w2.r, b2i, w2.i)
			if exceptional {
				s1r = kissMulSubSourceNonFinite(b2r, w2.r, b2i, w2.i)
			}
			s1i, exceptional := kissMulAddSource(b2r, w2.i, b2i, w2.r)
			if exceptional {
				s1i = kissMulAddSourceNonFinite(b2r, w2.i, b2i, w2.r)
			}
			s2r, exceptional := kissMulSubSource(b3r, w3.r, b3i, w3.i)
			if exceptional {
				s2r = kissMulSubSourceNonFinite(b3r, w3.r, b3i, w3.i)
			}
			s2i, exceptional := kissMulAddSource(b3r, w3.i, b3i, w3.r)
			if exceptional {
				s2i = kissMulAddSourceNonFinite(b3r, w3.i, b3i, w3.r)
			}

			s5r := f0r - s1r
			s5i := f0i - s1i
			f0r += s1r
			f0i += s1i

			s3r := s0r + s2r
			s3i := s0i + s2i
			s4r := s0r - s2r
			s4i := s0i - s2i

			fout[idx2] = f0r - s3r
			fout[idx2+1] = f0i - s3i

			tw1 += fstride
			tw2 += fstride2
			tw3 += fstride3

			f0r += s3r
			f0i += s3i
			fout[idx0] = f0r
			fout[idx0+1] = f0i

			fout[idx1] = s5r + s4i
			fout[idx1+1] = s5i - s4r
			fout[idx3] = s5r - s4i
			fout[idx3+1] = s5i + s4r
		}
	}
}
