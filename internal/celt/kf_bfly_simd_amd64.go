//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var kfBflyUsesAVX2 = archsimd.X86.AVX2() && (!celtAMD64SIMDUseFMA || archsimd.X86.FMA())
var kfBflyPerm0213U32x8 = [8]uint32{0, 1, 4, 5, 2, 3, 6, 7}

// The x86 inner butterflies process eight consecutive j values per block on
// AVX2 (when m is a multiple of 8) or four on AVX1. The block loop runs
// outside the N repeats so each block's gathered twiddles stay in registers
// across them; the butterflies are independent, so the order is free.
func kfBfly5InnerSIMD(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 {
		kfBfly5InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	if kfBflyUsesAVX2 && m >= 8 && m&7 == 0 {
		kfBfly5InnerAVX2(fout, w, m, N, mm, fstride)
		return
	}
	if !archsimd.X86.AVX() || m < 4 || m&3 != 0 {
		kfBfly5InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	kfBfly5InnerAVX(fout, w, m, N, mm, fstride)
}

//go:noinline
func kfBfly5InnerAVX2(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	ff := fout[:2*((N-1)*mm+5*m)]
	ya := w[fstride*m]
	yb := w[fstride*2*m]
	yar, yai := archsimd.BroadcastFloat32x8(ya.r), archsimd.BroadcastFloat32x8(ya.i)
	ybr, ybi := archsimd.BroadcastFloat32x8(yb.r), archsimd.BroadcastFloat32x8(yb.i)
	perm0213 := archsimd.LoadUint32x8Array(&kfBflyPerm0213U32x8)
	m2 := 2 * m

	for j := 0; j < m; j += 8 {
		w1r, w1i := bflyGatherTwiddle8AMD64(w, j*fstride, fstride)
		w2r, w2i := bflyGatherTwiddle8AMD64(w, j*2*fstride, 2*fstride)
		w3r, w3i := bflyGatherTwiddle8AMD64(w, j*3*fstride, 3*fstride)
		w4r, w4i := bflyGatherTwiddle8AMD64(w, j*4*fstride, 4*fstride)
		for i := 0; i < N; i++ {
			o0 := 2 * (i*mm + j)
			o1 := o0 + m2
			o2 := o1 + m2
			o3 := o2 + m2
			o4 := o3 + m2
			s0r, s0i := bflyLoadCpx8AMD64(ff[o0:], perm0213)
			b1r, b1i := bflyLoadCpx8AMD64(ff[o1:], perm0213)
			b2r, b2i := bflyLoadCpx8AMD64(ff[o2:], perm0213)
			b3r, b3i := bflyLoadCpx8AMD64(ff[o3:], perm0213)
			b4r, b4i := bflyLoadCpx8AMD64(ff[o4:], perm0213)

			s1r, s1i := bflyMulSource8AMD64(b1r, b1i, w1r, w1i)
			s2r, s2i := bflyMulSource8AMD64(b2r, b2i, w2r, w2i)
			s3r, s3i := bflyMulSource8AMD64(b3r, b3i, w3r, w3i)
			s4r, s4i := bflyMulSource8AMD64(b4r, b4i, w4r, w4i)
			s7r, s7i := s1r.Add(s4r), s1i.Add(s4i)
			s10r, s10i := s1r.Sub(s4r), s1i.Sub(s4i)
			s8r, s8i := s2r.Add(s3r), s2i.Add(s3i)
			s9r, s9i := s2r.Sub(s3r), s2i.Sub(s3i)

			bflyStoreCpx8AMD64(ff[o0:], s0r.Add(s7r.Add(s8r)), s0i.Add(s7i.Add(s8i)))
			s5r := s0r.Add(bflyMulAddSource8AMD64(s7r, yar, s8r, ybr))
			s5i := s0i.Add(bflyMulAddSource8AMD64(s7i, yar, s8i, ybr))
			s6r := bflyMulAddSource8AMD64(s10i, yai, s9i, ybi)
			s6i := bflyMulAddSource8AMD64(s10r, yai, s9r, ybi).Neg()
			bflyStoreCpx8AMD64(ff[o1:], s5r.Sub(s6r), s5i.Sub(s6i))
			bflyStoreCpx8AMD64(ff[o4:], s5r.Add(s6r), s5i.Add(s6i))

			s11r := s0r.Add(bflyMulAddSource8AMD64(s7r, ybr, s8r, yar))
			s11i := s0i.Add(bflyMulAddSource8AMD64(s7i, ybr, s8i, yar))
			s12r := bflyMulSubSource8AMD64(s9i, yai, s10i, ybi)
			s12i := bflyMulSubSource8AMD64(s10r, ybi, s9r, yai)
			bflyStoreCpx8AMD64(ff[o2:], s11r.Add(s12r), s11i.Add(s12i))
			bflyStoreCpx8AMD64(ff[o3:], s11r.Sub(s12r), s11i.Sub(s12i))
		}
	}
	archsimd.ClearAVXUpperBits()
}

//go:noinline
func kfBfly5InnerAVX(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	ff := fout[:2*((N-1)*mm+5*m)]
	ya := w[fstride*m]
	yb := w[fstride*2*m]
	yar, yai := broadcastF32x4Arch(ya.r), broadcastF32x4Arch(ya.i)
	ybr, ybi := broadcastF32x4Arch(yb.r), broadcastF32x4Arch(yb.i)
	m2 := 2 * m

	for j := 0; j < m; j += 4 {
		w1r, w1i := bflyGatherTwiddle4AMD64(w, j*fstride, fstride)
		w2r, w2i := bflyGatherTwiddle4AMD64(w, j*2*fstride, 2*fstride)
		w3r, w3i := bflyGatherTwiddle4AMD64(w, j*3*fstride, 3*fstride)
		w4r, w4i := bflyGatherTwiddle4AMD64(w, j*4*fstride, 4*fstride)
		for i := 0; i < N; i++ {
			o0 := 2 * (i*mm + j)
			o1 := o0 + m2
			o2 := o1 + m2
			o3 := o2 + m2
			o4 := o3 + m2
			s0r, s0i := bflyLoadCpx4AMD64(ff[o0:])
			b1r, b1i := bflyLoadCpx4AMD64(ff[o1:])
			b2r, b2i := bflyLoadCpx4AMD64(ff[o2:])
			b3r, b3i := bflyLoadCpx4AMD64(ff[o3:])
			b4r, b4i := bflyLoadCpx4AMD64(ff[o4:])

			s1r, s1i := bflyMulSource4AMD64(b1r, b1i, w1r, w1i)
			s2r, s2i := bflyMulSource4AMD64(b2r, b2i, w2r, w2i)
			s3r, s3i := bflyMulSource4AMD64(b3r, b3i, w3r, w3i)
			s4r, s4i := bflyMulSource4AMD64(b4r, b4i, w4r, w4i)
			s7r, s7i := s1r.Add(s4r), s1i.Add(s4i)
			s10r, s10i := s1r.Sub(s4r), s1i.Sub(s4i)
			s8r, s8i := s2r.Add(s3r), s2i.Add(s3i)
			s9r, s9i := s2r.Sub(s3r), s2i.Sub(s3i)

			bflyStoreCpx4AMD64(ff[o0:], s0r.Add(s7r.Add(s8r)), s0i.Add(s7i.Add(s8i)))
			s5r := s0r.Add(bflyMulAddSource4AMD64(s7r, yar, s8r, ybr))
			s5i := s0i.Add(bflyMulAddSource4AMD64(s7i, yar, s8i, ybr))
			s6r := bflyMulAddSource4AMD64(s10i, yai, s9i, ybi)
			s6i := negF32x4AVX(bflyMulAddSource4AMD64(s10r, yai, s9r, ybi))
			bflyStoreCpx4AMD64(ff[o1:], s5r.Sub(s6r), s5i.Sub(s6i))
			bflyStoreCpx4AMD64(ff[o4:], s5r.Add(s6r), s5i.Add(s6i))

			s11r := s0r.Add(bflyMulAddSource4AMD64(s7r, ybr, s8r, yar))
			s11i := s0i.Add(bflyMulAddSource4AMD64(s7i, ybr, s8i, yar))
			s12r := bflyMulSubSource4AMD64(s9i, yai, s10i, ybi)
			s12i := bflyMulSubSource4AMD64(s10r, ybi, s9r, yai)
			bflyStoreCpx4AMD64(ff[o2:], s11r.Add(s12r), s11i.Add(s12i))
			bflyStoreCpx4AMD64(ff[o3:], s11r.Sub(s12r), s11i.Sub(s12i))
		}
	}
}

func kfBfly4InnerSIMD(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 {
		kfBfly4InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	if kfBflyUsesAVX2 && m >= 8 && m&7 == 0 {
		kfBfly4InnerAVX2(fout, w, m, N, mm, fstride)
		return
	}
	if !archsimd.X86.AVX() || m < 4 || m&3 != 0 {
		kfBfly4InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	kfBfly4InnerAVX(fout, w, m, N, mm, fstride)
}

//go:noinline
func kfBfly4InnerAVX2(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	ff := fout[:2*((N-1)*mm+4*m)]
	perm0213 := archsimd.LoadUint32x8Array(&kfBflyPerm0213U32x8)
	m2 := 2 * m
	for j := 0; j < m; j += 8 {
		w1r, w1i := bflyGatherTwiddle8AMD64(w, j*fstride, fstride)
		w2r, w2i := bflyGatherTwiddle8AMD64(w, j*2*fstride, 2*fstride)
		w3r, w3i := bflyGatherTwiddle8AMD64(w, j*3*fstride, 3*fstride)
		for i := 0; i < N; i++ {
			o0 := 2 * (i*mm + j)
			o1 := o0 + m2
			o2 := o1 + m2
			o3 := o2 + m2
			f0r, f0i := bflyLoadCpx8AMD64(ff[o0:], perm0213)
			b1r, b1i := bflyLoadCpx8AMD64(ff[o1:], perm0213)
			b2r, b2i := bflyLoadCpx8AMD64(ff[o2:], perm0213)
			b3r, b3i := bflyLoadCpx8AMD64(ff[o3:], perm0213)

			s0ur, s0ui := bflyMulSource8UnfusedAMD64(b1r, b1i, w1r, w1i)
			s1r, s1i := bflyMulSource8UnfusedAMD64(b2r, b2i, w2r, w2i)
			s2ur, s2ui := bflyMulSource8UnfusedAMD64(b3r, b3i, w3r, w3i)
			s0fr, s0fi := bflyMulSource8FusedAMD64(b1r, b1i, w1r, w1i)
			s2fr, s2fi := bflyMulSource8FusedAMD64(b3r, b3i, w3r, w3i)
			s5r, s5i := f0r.Sub(s1r), f0i.Sub(s1i)
			f0r, f0i = f0r.Add(s1r), f0i.Add(s1i)
			s3r, s3i := s0ur.Add(s2ur), s0ui.Add(s2ui)
			s4r, s4i := s0fr.Sub(s2fr), s0fi.Sub(s2fi)
			out2r, out2i := f0r.Sub(s3r), f0i.Sub(s3i)
			out0r, out0i := f0r.Add(s3r), f0i.Add(s3i)
			out1r, out1i := s5r.Add(s4i), s5i.Sub(s4r)
			out3r, out3i := s5r.Sub(s4i), s5i.Add(s4r)
			bflyStoreCpx8AMD64(ff[o2:], out2r, out2i)
			bflyStoreCpx8AMD64(ff[o0:], out0r, out0i)
			bflyStoreCpx8AMD64(ff[o1:], out1r, out1i)
			bflyStoreCpx8AMD64(ff[o3:], out3r, out3i)
		}
	}
	archsimd.ClearAVXUpperBits()
}

//go:noinline
func kfBfly4InnerAVX(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	ff := fout[:2*((N-1)*mm+4*m)]
	m2 := 2 * m
	for j := 0; j < m; j += 4 {
		w1r, w1i := bflyGatherTwiddle4AMD64(w, j*fstride, fstride)
		w2r, w2i := bflyGatherTwiddle4AMD64(w, j*2*fstride, 2*fstride)
		w3r, w3i := bflyGatherTwiddle4AMD64(w, j*3*fstride, 3*fstride)
		for i := 0; i < N; i++ {
			o0 := 2 * (i*mm + j)
			o1 := o0 + m2
			o2 := o1 + m2
			o3 := o2 + m2
			f0r, f0i := bflyLoadCpx4AMD64(ff[o0:])
			b1r, b1i := bflyLoadCpx4AMD64(ff[o1:])
			b2r, b2i := bflyLoadCpx4AMD64(ff[o2:])
			b3r, b3i := bflyLoadCpx4AMD64(ff[o3:])

			s0ur, s0ui := bflyMulSource4UnfusedAMD64(b1r, b1i, w1r, w1i)
			s1r, s1i := bflyMulSource4UnfusedAMD64(b2r, b2i, w2r, w2i)
			s2ur, s2ui := bflyMulSource4UnfusedAMD64(b3r, b3i, w3r, w3i)
			s0fr, s0fi := bflyMulSource4FusedAMD64(b1r, b1i, w1r, w1i)
			s2fr, s2fi := bflyMulSource4FusedAMD64(b3r, b3i, w3r, w3i)
			s5r, s5i := f0r.Sub(s1r), f0i.Sub(s1i)
			f0r, f0i = f0r.Add(s1r), f0i.Add(s1i)
			s3r, s3i := s0ur.Add(s2ur), s0ui.Add(s2ui)
			s4r, s4i := s0fr.Sub(s2fr), s0fi.Sub(s2fi)
			out2r, out2i := f0r.Sub(s3r), f0i.Sub(s3i)
			out0r, out0i := f0r.Add(s3r), f0i.Add(s3i)
			out1r, out1i := s5r.Add(s4i), s5i.Sub(s4r)
			out3r, out3i := s5r.Sub(s4i), s5i.Add(s4r)
			bflyStoreCpx4AMD64(ff[o2:], out2r, out2i)
			bflyStoreCpx4AMD64(ff[o0:], out0r, out0i)
			bflyStoreCpx4AMD64(ff[o1:], out1r, out1i)
			bflyStoreCpx4AMD64(ff[o3:], out3r, out3i)
		}
	}
}

func kfBfly3InnerSIMD(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	if m <= 0 || N <= 0 {
		kfBfly3InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	if kfBflyUsesAVX2 && m >= 8 && m&7 == 0 {
		kfBfly3InnerAVX2(fout, w, m, N, mm, fstride)
		return
	}
	if !archsimd.X86.AVX() || m&3 != 0 {
		kfBfly3InnerScalar(fout, w, m, N, mm, fstride)
		return
	}
	kfBfly3InnerAVX(fout, w, m, N, mm, fstride)
}

//go:noinline
func kfBfly3InnerAVX2(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	ff := fout[:2*((N-1)*mm+3*m)]
	_ = w[2*(m-1)*fstride]

	epi3i := archsimd.BroadcastFloat32x8(w[fstride*m].i)
	half := archsimd.BroadcastFloat32x8(0.5)
	perm0213 := archsimd.LoadUint32x8Array(&kfBflyPerm0213U32x8)
	m2 := 2 * m
	for j := 0; j < m; j += 8 {
		w1r, w1i := bflyGatherTwiddle8AMD64(w, j*fstride, fstride)
		w2r, w2i := bflyGatherTwiddle8AMD64(w, j*2*fstride, 2*fstride)
		for i := 0; i < N; i++ {
			o0 := 2 * (i*mm + j)
			o1 := o0 + m2
			o2 := o1 + m2
			a0r, a0i := bflyLoadCpx8AMD64(ff[o0:], perm0213)
			b1r, b1i := bflyLoadCpx8AMD64(ff[o1:], perm0213)
			b2r, b2i := bflyLoadCpx8AMD64(ff[o2:], perm0213)

			s1r, s1i := bflyMulSource8AMD64(b1r, b1i, w1r, w1i)
			s2r, s2i := bflyMulSource8AMD64(b2r, b2i, w2r, w2i)
			s3r, s3i := s1r.Add(s2r), s1i.Add(s2i)
			s0r, s0i := s1r.Sub(s2r), s1i.Sub(s2i)
			f1r, f1i := bflyHalfSub8AMD64(a0r, s3r, half), bflyHalfSub8AMD64(a0i, s3i, half)
			s0r, s0i = s0r.Mul(epi3i), s0i.Mul(epi3i)
			bflyStoreCpx8AMD64(ff[o0:], a0r.Add(s3r), a0i.Add(s3i))
			bflyStoreCpx8AMD64(ff[o2:], f1r.Add(s0i), f1i.Sub(s0r))
			bflyStoreCpx8AMD64(ff[o1:], f1r.Sub(s0i), f1i.Add(s0r))
		}
	}
	archsimd.ClearAVXUpperBits()
}

//go:noinline
func kfBfly3InnerAVX(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	ff := fout[:2*((N-1)*mm+3*m)]
	_ = w[2*(m-1)*fstride]

	epi3i := broadcastF32x4Arch(w[fstride*m].i)
	half := broadcastF32x4Arch(0.5)
	m2 := 2 * m
	for j := 0; j < m; j += 4 {
		w1r, w1i := bflyGatherTwiddle4AMD64(w, j*fstride, fstride)
		w2r, w2i := bflyGatherTwiddle4AMD64(w, j*2*fstride, 2*fstride)
		for i := 0; i < N; i++ {
			o0 := 2 * (i*mm + j)
			o1 := o0 + m2
			o2 := o1 + m2
			a0r, a0i := bflyLoadCpx4AMD64(ff[o0:])
			b1r, b1i := bflyLoadCpx4AMD64(ff[o1:])
			b2r, b2i := bflyLoadCpx4AMD64(ff[o2:])

			s1r, s1i := bflyMulSource4AMD64(b1r, b1i, w1r, w1i)
			s2r, s2i := bflyMulSource4AMD64(b2r, b2i, w2r, w2i)
			s3r, s3i := s1r.Add(s2r), s1i.Add(s2i)
			s0r, s0i := s1r.Sub(s2r), s1i.Sub(s2i)
			f1r, f1i := bflyHalfSub4AMD64(a0r, s3r, half), bflyHalfSub4AMD64(a0i, s3i, half)
			s0r, s0i = s0r.Mul(epi3i), s0i.Mul(epi3i)
			bflyStoreCpx4AMD64(ff[o0:], a0r.Add(s3r), a0i.Add(s3i))
			bflyStoreCpx4AMD64(ff[o2:], f1r.Add(s0i), f1i.Sub(s0r))
			bflyStoreCpx4AMD64(ff[o1:], f1r.Sub(s0i), f1i.Add(s0r))
		}
	}
}

// kfBfly4M1CoreSIMD runs radix-4 m==1 butterflies on interleaved vectors
// {a0,a1} and {a2,a3}.
func kfBfly4M1CoreSIMD(fout []float32, n int) {
	if n <= 0 || !archsimd.X86.AVX() {
		kfBfly4M1CoreScalar(fout, n)
		return
	}
	ff := fout[:8*n]
	lane3 := archsimd.LoadInt32x4Array(&[4]int32{0, 0, 0, -1}).ToMask()
	for i := range n {
		blk := ff[i*8 : i*8+8]
		x := archsimd.LoadFloat32x4Array((*[4]float32)(blk[0:]))
		y := archsimd.LoadFloat32x4Array((*[4]float32)(blk[4:]))
		sum := x.Add(y)
		diff := x.Sub(y)
		a := sum.ConcatPermuteScalars(0, 1, 4, 5, diff)
		b := sum.ConcatPermuteScalars(2, 3, 7, 6, diff)
		plus := a.Add(b)
		minus := a.Sub(b)
		minus.IfElse(lane3, plus).StoreArray((*[4]float32)(blk[0:]))
		plus.IfElse(lane3, minus).StoreArray((*[4]float32)(blk[4:]))
	}
}

func bflyLoadCpx4AMD64(s []float32) (re, im archsimd.Float32x4) {
	lo := archsimd.LoadFloat32x4Array((*[4]float32)(s[0:]))
	hi := archsimd.LoadFloat32x4Array((*[4]float32)(s[4:]))
	return lo.ConcatPermuteScalars(0, 2, 4, 6, hi), lo.ConcatPermuteScalars(1, 3, 5, 7, hi)
}

func bflyStoreCpx4AMD64(s []float32, re, im archsimd.Float32x4) {
	reBits, imBits := re.ToBits(), im.ToBits()
	reBits.InterleaveLo(imBits).BitsToFloat32().StoreArray((*[4]float32)(s[0:]))
	reBits.InterleaveHi(imBits).BitsToFloat32().StoreArray((*[4]float32)(s[4:]))
}

func bflyGatherTwiddle4AMD64(w []kissCpx, start, stride int) (re, im archsimd.Float32x4) {
	t0 := w[start]
	t1 := w[start+stride]
	t2 := w[start+2*stride]
	t3 := w[start+3*stride]
	re = broadcastF32x4Arch(t0.r).SetElem(1, t1.r).SetElem(2, t2.r).SetElem(3, t3.r)
	im = broadcastF32x4Arch(t0.i).SetElem(1, t1.i).SetElem(2, t2.i).SetElem(3, t3.i)
	return re, im
}

func bflyLoadCpx8AMD64(s []float32, perm0213 archsimd.Uint32x8) (re, im archsimd.Float32x8) {
	lo := archsimd.LoadFloat32x8Array((*[8]float32)(s[0:]))
	hi := archsimd.LoadFloat32x8Array((*[8]float32)(s[8:]))
	re = lo.ConcatPermuteScalarsGrouped(0, 2, 4, 6, hi).Permute(perm0213)
	im = lo.ConcatPermuteScalarsGrouped(1, 3, 5, 7, hi).Permute(perm0213)
	return re, im
}

func bflyStoreCpx8AMD64(s []float32, re, im archsimd.Float32x8) {
	reU, imU := re.AsUint32x8(), im.AsUint32x8()
	ilLo := reU.InterleaveLoGrouped(imU).AsFloat32x8()
	ilHi := reU.InterleaveHiGrouped(imU).AsFloat32x8()
	ilLo.ConcatPermute128Scalars(0, 2, ilHi).StoreArray((*[8]float32)(s[0:]))
	ilLo.ConcatPermute128Scalars(1, 3, ilHi).StoreArray((*[8]float32)(s[8:]))
}

func bflyGatherTwiddle8AMD64(w []kissCpx, start, stride int) (re, im archsimd.Float32x8) {
	t0 := w[start]
	t1 := w[start+stride]
	t2 := w[start+2*stride]
	t3 := w[start+3*stride]
	t4 := w[start+4*stride]
	t5 := w[start+5*stride]
	t6 := w[start+6*stride]
	t7 := w[start+7*stride]
	re = archsimd.LoadFloat32x8Array(&[8]float32{t0.r, t1.r, t2.r, t3.r, t4.r, t5.r, t6.r, t7.r})
	im = archsimd.LoadFloat32x8Array(&[8]float32{t0.i, t1.i, t2.i, t3.i, t4.i, t5.i, t6.i, t7.i})
	return re, im
}
