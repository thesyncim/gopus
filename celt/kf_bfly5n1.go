package celt

func kfBfly5N1Generic(fout []kissCpx, tw []kissCpx, m, fstride int) {
	if m <= 0 || fstride <= 0 {
		return
	}
	last := 5*m - 1
	needTw := 2 * fstride * m
	if tw4Need := 4 * fstride * (m - 1); tw4Need > needTw {
		needTw = tw4Need
	}
	if last >= len(fout) || needTw >= len(tw) {
		return
	}
	ya := tw[fstride*m]
	yb := tw[2*fstride*m]
	yar, yai := ya.r, ya.i
	ybr, ybi := yb.r, yb.i

	idx0, idx1, idx2, idx3, idx4 := 0, m, 2*m, 3*m, 4*m
	tw1, tw2, tw3, tw4 := 0, 0, 0, 0
	fstride2 := fstride * 2
	fstride3 := fstride * 3
	fstride4 := fstride * 4

	for u := 0; u < m; u++ {
		s0 := fout[idx0]
		b1 := fout[idx1]
		b2 := fout[idx2]
		b3 := fout[idx3]
		b4 := fout[idx4]
		w1 := tw[tw1]
		w2 := tw[tw2]
		w3 := tw[tw3]
		w4 := tw[tw4]

		s1r := kissMulSubSource(b1.r, w1.r, b1.i, w1.i)
		s1i := kissMulAddSource(b1.r, w1.i, b1.i, w1.r)
		s2r := kissMulSubSource(b2.r, w2.r, b2.i, w2.i)
		s2i := kissMulAddSource(b2.r, w2.i, b2.i, w2.r)
		s3r := kissMulSubSource(b3.r, w3.r, b3.i, w3.i)
		s3i := kissMulAddSource(b3.r, w3.i, b3.i, w3.r)
		s4r := kissMulSubSource(b4.r, w4.r, b4.i, w4.i)
		s4i := kissMulAddSource(b4.r, w4.i, b4.i, w4.r)

		s7r, s7i := s1r+s4r, s1i+s4i
		s10r, s10i := s1r-s4r, s1i-s4i
		s8r, s8i := s2r+s3r, s2i+s3i
		s9r, s9i := s2r-s3r, s2i-s3i

		fout[idx0].r = s0.r + s7r + s8r
		fout[idx0].i = s0.i + s7i + s8i

		s5r := s0.r + kissMulAddSource(s7r, yar, s8r, ybr)
		s5i := s0.i + kissMulAddSource(s7i, yar, s8i, ybr)
		s6r := kissMulAddSource(s10i, yai, s9i, ybi)
		s6i := -kissMulAddSource(s10r, yai, s9r, ybi)
		fout[idx1].r, fout[idx1].i = s5r-s6r, s5i-s6i
		fout[idx4].r, fout[idx4].i = s5r+s6r, s5i+s6i

		s11r := s0.r + kissMulAddSource(s7r, ybr, s8r, yar)
		s11i := s0.i + kissMulAddSource(s7i, ybr, s8i, yar)
		s12r := kissMulSubSource(s9i, yai, s10i, ybi)
		s12i := kissMulSubSource(s10r, ybi, s9r, yai)
		fout[idx2].r, fout[idx2].i = s11r+s12r, s11i+s12i
		fout[idx3].r, fout[idx3].i = s11r-s12r, s11i-s12i

		idx0++
		idx1++
		idx2++
		idx3++
		idx4++
		tw1 += fstride
		tw2 += fstride2
		tw3 += fstride3
		tw4 += fstride4
	}
}
