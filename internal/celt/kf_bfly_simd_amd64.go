//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// The x86 inner butterflies process four consecutive j values per block.
// AVX supplies the shuffle operations used to separate and reinterleave the
// real and imaginary lanes; CPUs without AVX and stages off this grid use the
// scalar implementation. The arithmetic keeps the separate float32 multiply
// and add/subtract operations used by libopus's x86 path.
func kfBfly5InnerSIMD(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 {
		kfBfly5InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	if !archsimd.X86.AVX() || m < 4 || m&3 != 0 {
		kfBfly5InnerScalar(fout, w, m, N, mm, fstride)
		return
	}

	wBase := unsafe.Pointer(unsafe.SliceData(w))
	ya := *(*kissCpx)(unsafe.Add(wBase, fstride*m*8))
	yb := *(*kissCpx)(unsafe.Add(wBase, fstride*2*m*8))
	yar, yai := archsimd.BroadcastFloat32x4(ya.r), archsimd.BroadcastFloat32x4(ya.i)
	ybr, ybi := archsimd.BroadcastFloat32x4(yb.r), archsimd.BroadcastFloat32x4(yb.i)
	foutBase := unsafe.Pointer(unsafe.SliceData(fout))

	for i := 0; i < N; i++ {
		base := i * mm
		for j := 0; j < m; j += 4 {
			p0 := unsafe.Add(foutBase, (base+j)*8)
			p1 := unsafe.Add(p0, m*8)
			p2 := unsafe.Add(p1, m*8)
			p3 := unsafe.Add(p2, m*8)
			p4 := unsafe.Add(p3, m*8)
			s0r, s0i := bflyLoadCpx4AMD64(p0)
			b1r, b1i := bflyLoadCpx4AMD64(p1)
			b2r, b2i := bflyLoadCpx4AMD64(p2)
			b3r, b3i := bflyLoadCpx4AMD64(p3)
			b4r, b4i := bflyLoadCpx4AMD64(p4)
			w1r, w1i := bflyGatherTwiddle4AMD64(wBase, j*fstride, fstride)
			w2r, w2i := bflyGatherTwiddle4AMD64(wBase, j*2*fstride, 2*fstride)
			w3r, w3i := bflyGatherTwiddle4AMD64(wBase, j*3*fstride, 3*fstride)
			w4r, w4i := bflyGatherTwiddle4AMD64(wBase, j*4*fstride, 4*fstride)

			s1r, s1i := bflyMulSource4AMD64(b1r, b1i, w1r, w1i)
			s2r, s2i := bflyMulSource4AMD64(b2r, b2i, w2r, w2i)
			s3r, s3i := bflyMulSource4AMD64(b3r, b3i, w3r, w3i)
			s4r, s4i := bflyMulSource4AMD64(b4r, b4i, w4r, w4i)
			s7r, s7i := s1r.Add(s4r), s1i.Add(s4i)
			s10r, s10i := s1r.Sub(s4r), s1i.Sub(s4i)
			s8r, s8i := s2r.Add(s3r), s2i.Add(s3i)
			s9r, s9i := s2r.Sub(s3r), s2i.Sub(s3i)

			bflyStoreCpx4AMD64(p0, s0r.Add(s7r.Add(s8r)), s0i.Add(s7i.Add(s8i)))
			s5r := s0r.Add(s7r.Mul(yar).Add(s8r.Mul(ybr)))
			s5i := s0i.Add(s7i.Mul(yar).Add(s8i.Mul(ybr)))
			s6r := s10i.Mul(yai).Add(s9i.Mul(ybi))
			s6i := s10r.Mul(yai).Add(s9r.Mul(ybi)).Neg()
			bflyStoreCpx4AMD64(p1, s5r.Sub(s6r), s5i.Sub(s6i))
			bflyStoreCpx4AMD64(p4, s5r.Add(s6r), s5i.Add(s6i))

			s11r := s0r.Add(s7r.Mul(ybr).Add(s8r.Mul(yar)))
			s11i := s0i.Add(s7i.Mul(ybr).Add(s8i.Mul(yar)))
			s12r := s9i.Mul(yai).Sub(s10i.Mul(ybi))
			s12i := s10r.Mul(ybi).Sub(s9r.Mul(yai))
			bflyStoreCpx4AMD64(p2, s11r.Add(s12r), s11i.Add(s12i))
			bflyStoreCpx4AMD64(p3, s11r.Sub(s12r), s11i.Sub(s12i))
		}
	}
}

func kfBfly4InnerSIMD(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 {
		kfBfly4InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	if !archsimd.X86.AVX() || m < 4 || m&3 != 0 {
		kfBfly4InnerScalar(fout, w, m, N, mm, fstride)
		return
	}

	wBase := unsafe.Pointer(unsafe.SliceData(w))
	foutBase := unsafe.Pointer(unsafe.SliceData(fout))
	for i := 0; i < N; i++ {
		base := i * mm
		for j := 0; j < m; j += 4 {
			p0 := unsafe.Add(foutBase, (base+j)*8)
			p1 := unsafe.Add(p0, m*8)
			p2 := unsafe.Add(p1, m*8)
			p3 := unsafe.Add(p2, m*8)
			f0r, f0i := bflyLoadCpx4AMD64(p0)
			b1r, b1i := bflyLoadCpx4AMD64(p1)
			b2r, b2i := bflyLoadCpx4AMD64(p2)
			b3r, b3i := bflyLoadCpx4AMD64(p3)
			w1r, w1i := bflyGatherTwiddle4AMD64(wBase, j*fstride, fstride)
			w2r, w2i := bflyGatherTwiddle4AMD64(wBase, j*2*fstride, 2*fstride)
			w3r, w3i := bflyGatherTwiddle4AMD64(wBase, j*3*fstride, 3*fstride)

			s0r, s0i := bflyMulSource4AMD64(b1r, b1i, w1r, w1i)
			s1r, s1i := bflyMulSource4AMD64(b2r, b2i, w2r, w2i)
			s2r, s2i := bflyMulSource4AMD64(b3r, b3i, w3r, w3i)
			s5r, s5i := f0r.Sub(s1r), f0i.Sub(s1i)
			f0r, f0i = f0r.Add(s1r), f0i.Add(s1i)
			s3r, s3i := s0r.Add(s2r), s0i.Add(s2i)
			s4r, s4i := s0r.Sub(s2r), s0i.Sub(s2i)
			out2r, out2i := f0r.Sub(s3r), f0i.Sub(s3i)
			out0r, out0i := f0r.Add(s3r), f0i.Add(s3i)
			out1r, out1i := s5r.Add(s4i), s5i.Sub(s4r)
			out3r, out3i := s5r.Sub(s4i), s5i.Add(s4r)
			bflyStoreCpx4AMD64(p2, out2r, out2i)
			bflyStoreCpx4AMD64(p0, out0r, out0i)
			bflyStoreCpx4AMD64(p1, out1r, out1i)
			bflyStoreCpx4AMD64(p3, out3r, out3i)
		}
	}
}

func bflyLoadCpx4AMD64(p unsafe.Pointer) (re, im archsimd.Float32x4) {
	lo := loadF32x4(p)
	hi := loadF32x4(unsafe.Add(p, 16))
	return lo.ConcatPermuteScalars(0, 2, 4, 6, hi), lo.ConcatPermuteScalars(1, 3, 5, 7, hi)
}

func bflyStoreCpx4AMD64(p unsafe.Pointer, re, im archsimd.Float32x4) {
	reBits, imBits := re.ToBits(), im.ToBits()
	storeF32x4(p, reBits.InterleaveLo(imBits).BitsToFloat32())
	storeF32x4(unsafe.Add(p, 16), reBits.InterleaveHi(imBits).BitsToFloat32())
}

func bflyGatherTwiddle4AMD64(wBase unsafe.Pointer, start, stride int) (re, im archsimd.Float32x4) {
	t0 := *(*kissCpx)(unsafe.Add(wBase, start*8))
	t1 := *(*kissCpx)(unsafe.Add(wBase, (start+stride)*8))
	t2 := *(*kissCpx)(unsafe.Add(wBase, (start+2*stride)*8))
	t3 := *(*kissCpx)(unsafe.Add(wBase, (start+3*stride)*8))
	re = archsimd.BroadcastFloat32x4(t0.r).SetElem(1, t1.r).SetElem(2, t2.r).SetElem(3, t3.r)
	im = archsimd.BroadcastFloat32x4(t0.i).SetElem(1, t1.i).SetElem(2, t2.i).SetElem(3, t3.i)
	return re, im
}

func bflyMulSource4AMD64(ar, ai, wr, wi archsimd.Float32x4) (re, im archsimd.Float32x4) {
	re = ar.Mul(wr).Sub(ai.Mul(wi))
	im = ar.Mul(wi).Add(ai.Mul(wr))
	return re, im
}
