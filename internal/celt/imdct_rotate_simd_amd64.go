//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// The x86 libopus SIMD pre-rotation uses separate multiplies and add/subtract.
// The post-rotation uses separate products through AMD64 v2 and fuses the
// first product on AMD64 v3, so the lane kernels follow that target shape.

func evenLanes4(lo, hi archsimd.Float32x4) archsimd.Float32x4 {
	return lo.ConcatPermuteScalars(0, 2, 4, 6, hi)
}

func oddLanes4(lo, hi archsimd.Float32x4) archsimd.Float32x4 {
	return lo.ConcatPermuteScalars(1, 3, 5, 7, hi)
}

func reverseLanes4(v archsimd.Float32x4) archsimd.Float32x4 {
	return v.ConcatPermuteScalars(3, 2, 5, 4, v)
}

func storeInterleaved4(dst []float32, even, odd archsimd.Float32x4) {
	e, o := even.ToBits(), odd.ToBits()
	e.InterleaveLo(o).BitsToFloat32().StoreArray((*[4]float32)(dst[0:]))
	e.InterleaveHi(o).BitsToFloat32().StoreArray((*[4]float32)(dst[4:]))
}

// imdctPostRotateF32FromKiss computes, per iteration i (k = n4-1-i), with
// re=fft[i].i, im=fft[i].r, re2=fft[k].i, im2=fft[k].r:
//
//	buf[2i]      = re*trig[i]       + im*trig[n4+i]
//	buf[n2-1-2i] = re*trig[n4+i]    - im*trig[i]
//	buf[n2-2-2i] = re2*trig[n4-1-i] + im2*trig[n2-1-i]
//	buf[2i+1]    = re2*trig[n2-1-i] - im2*trig[n4-1-i]
//
// eight iterations per block on AVX2, four on AVX1, then scalar for limit%4.
func imdctPostRotateF32FromKiss(buf []float32, fft []float32, trig []float32, n2, n4 int) {
	if len(buf) < n2 || len(fft) < 2*n4 {
		return
	}
	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}
	if imdctTDACUsesAVX2 && limit >= 8 {
		imdctPostRotateF32FromKissAVX2(buf, fft, trig, n2, n4, limit)
		return
	}
	if !archsimd.X86.AVX() {
		imdctPostRotateF32FromKissScalar(buf, fft, trig, n2, n4)
		return
	}
	imdctPostRotateF32FromKissAVX(buf, fft, trig, n2, n4, limit)
}

//go:noinline
func imdctPostRotateF32FromKissAVX2(buf []float32, fft []float32, trig []float32, n2, n4, limit int) {
	buf = buf[:n2]
	ff := fft[:2*n4]
	trig = trig[:n2]
	perm0213 := archsimd.LoadUint32x8Array(&kfBflyPerm0213U32x8)

	i := 0
	for ; i+8 <= limit; i += 8 {
		im, re := bflyLoadCpx8AMD64(ff[2*i:], perm0213)
		t0 := archsimd.LoadFloat32x8Array((*[8]float32)(trig[i:]))
		t1 := archsimd.LoadFloat32x8Array((*[8]float32)(trig[n4+i:]))
		yr := re.Mul(t0).Add(im.Mul(t1))
		yi := re.Mul(t1).Sub(im.Mul(t0))
		if mdctUseFMALikeMixEnabled {
			yr = re.MulAdd(t0, im.Mul(t1))
			yi = re.MulAdd(t1, im.Mul(t0).Neg())
		}

		kbase := n4 - 8 - i
		im2Raw, re2Raw := bflyLoadCpx8AMD64(ff[2*kbase:], perm0213)
		im2 := reverseF32x8(im2Raw)
		re2 := reverseF32x8(re2Raw)
		t0b := reverseF32x8(archsimd.LoadFloat32x8Array((*[8]float32)(trig[kbase:])))
		t1b := reverseF32x8(archsimd.LoadFloat32x8Array((*[8]float32)(trig[n2-8-i:])))
		yr2 := re2.Mul(t0b).Add(im2.Mul(t1b))
		yi2 := re2.Mul(t1b).Sub(im2.Mul(t0b))
		if mdctUseFMALikeMixEnabled {
			yr2 = re2.MulAdd(t0b, im2.Mul(t1b))
			yi2 = re2.MulAdd(t1b, im2.Mul(t0b).Neg())
		}

		bflyStoreCpx8AMD64(buf[2*i:], yr, yi2)
		bflyStoreCpx8AMD64(buf[n2-16-2*i:], reverseF32x8(yr2), reverseF32x8(yi))
	}
	archsimd.ClearAVXUpperBits()
	for ; i+4 <= limit; i += 4 {
		f0 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*i:]))
		f1 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*i+4:]))
		im, re := evenLanes4(f0, f1), oddLanes4(f0, f1)
		t0 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:]))
		t1 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:]))
		yr := re.Mul(t0).Add(im.Mul(t1))
		yi := re.Mul(t1).Sub(im.Mul(t0))
		if mdctUseFMALikeMixEnabled {
			yr = re.MulAdd(t0, im.Mul(t1))
			yi = re.MulAdd(t1, im.Mul(t0).Neg())
		}

		kbase := n4 - 4 - i
		g0 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*kbase:]))
		g1 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*kbase+4:]))
		im2 := reverseLanes4(evenLanes4(g0, g1))
		re2 := reverseLanes4(oddLanes4(g0, g1))
		t0b := reverseLanes4(archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4-4-i:])))
		t1b := reverseLanes4(archsimd.LoadFloat32x4Array((*[4]float32)(trig[n2-4-i:])))
		yr2 := re2.Mul(t0b).Add(im2.Mul(t1b))
		yi2 := re2.Mul(t1b).Sub(im2.Mul(t0b))
		if mdctUseFMALikeMixEnabled {
			yr2 = re2.MulAdd(t0b, im2.Mul(t1b))
			yi2 = re2.MulAdd(t1b, im2.Mul(t0b).Neg())
		}

		storeInterleaved4(buf[2*i:], yr, yi2)
		storeInterleaved4(buf[n2-8-2*i:], reverseLanes4(yr2), reverseLanes4(yi))
	}
	for ; i < limit; i++ {
		k := n4 - 1 - i
		re, im := ff[2*i+1], ff[2*i]
		t0, t1 := trig[i], trig[n4+i]
		buf[2*i] = mdctMulAddMix(re, im, t0, t1)
		buf[n2-1-2*i] = mdctMulSubMix(re, im, t1, t0)
		re2, im2 := ff[2*k+1], ff[2*k]
		t0, t1 = trig[n4-i-1], trig[n2-i-1]
		buf[n2-2-2*i] = mdctMulAddMix(re2, im2, t0, t1)
		buf[2*i+1] = mdctMulSubMix(re2, im2, t1, t0)
	}
}

//go:noinline
func imdctPostRotateF32FromKissAVX(buf []float32, fft []float32, trig []float32, n2, n4, limit int) {
	buf = buf[:n2]
	ff := fft[:2*n4]
	trig = trig[:n2]

	i := 0
	for ; i+4 <= limit; i += 4 {
		f0 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*i:]))
		f1 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*i+4:]))
		im, re := evenLanes4(f0, f1), oddLanes4(f0, f1)
		t0 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:]))
		t1 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:]))
		yr := re.Mul(t0).Add(im.Mul(t1))
		yi := re.Mul(t1).Sub(im.Mul(t0))
		if mdctUseFMALikeMixEnabled {
			yr = re.MulAdd(t0, im.Mul(t1))
			yi = re.MulAdd(t1, im.Mul(t0).Neg())
		}

		kbase := n4 - 4 - i
		g0 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*kbase:]))
		g1 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*kbase+4:]))
		im2 := reverseLanes4(evenLanes4(g0, g1))
		re2 := reverseLanes4(oddLanes4(g0, g1))
		t0b := reverseLanes4(archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4-4-i:])))
		t1b := reverseLanes4(archsimd.LoadFloat32x4Array((*[4]float32)(trig[n2-4-i:])))
		yr2 := re2.Mul(t0b).Add(im2.Mul(t1b))
		yi2 := re2.Mul(t1b).Sub(im2.Mul(t0b))
		if mdctUseFMALikeMixEnabled {
			yr2 = re2.MulAdd(t0b, im2.Mul(t1b))
			yi2 = re2.MulAdd(t1b, im2.Mul(t0b).Neg())
		}

		storeInterleaved4(buf[2*i:], yr, yi2)
		storeInterleaved4(buf[n2-8-2*i:], reverseLanes4(yr2), reverseLanes4(yi))
	}
	for ; i < limit; i++ {
		k := n4 - 1 - i
		re, im := ff[2*i+1], ff[2*i]
		t0, t1 := trig[i], trig[n4+i]
		buf[2*i] = mdctMulAddMix(re, im, t0, t1)
		buf[n2-1-2*i] = mdctMulSubMix(re, im, t1, t0)
		re2, im2 := ff[2*k+1], ff[2*k]
		t0, t1 = trig[n4-i-1], trig[n2-i-1]
		buf[n2-2-2*i] = mdctMulAddMix(re2, im2, t0, t1)
		buf[2*i+1] = mdctMulSubMix(re2, im2, t1, t0)
	}
}

// imdctPreRotateNoFMA computes fftIn[2*i..2*i+1] = (x1*t0 - x2*t1, x2*t0 + x1*t1) with
// x1 = spectrum[2i], x2 = spectrum[n2-1-2i], t0 = trig[i], t1 = trig[n4+i],
// eight i per block on AVX2, four on AVX1, then scalar for n4%4.
func imdctPreRotateNoFMA(fftIn []float32, spectrum []float32, trig []float32, n2, n4 int) {
	if preemphUsesAVX2 && n4 >= 8 {
		imdctPreRotateNoFMAAVX2(fftIn, spectrum, trig, n2, n4)
		return
	}
	if !archsimd.X86.AVX() {
		imdctPreRotateNoFMAScalar(fftIn, spectrum, trig, n2, n4)
		return
	}
	imdctPreRotateNoFMAAVX(fftIn, spectrum, trig, n2, n4)
}

//go:noinline
func imdctPreRotateNoFMAAVX2(fftIn []float32, spectrum []float32, trig []float32, n2, n4 int) {
	spectrum = spectrum[:max(n2, 2*n4)]
	trig = trig[:n2]
	outF := fftIn[:2*n4]
	perm0213 := archsimd.LoadUint32x8Array(&kfBflyPerm0213U32x8)

	i := 0
	for ; i+8 <= n4; i += 8 {
		x1, _ := bflyLoadCpx8AMD64(spectrum[2*i:], perm0213)
		b := n2 - 16 - 2*i
		_, x2Raw := bflyLoadCpx8AMD64(spectrum[b:], perm0213)
		x2 := reverseF32x8(x2Raw)
		t0 := archsimd.LoadFloat32x8Array((*[8]float32)(trig[i:]))
		t1 := archsimd.LoadFloat32x8Array((*[8]float32)(trig[n4+i:]))
		re := x1.Mul(t0).Sub(x2.Mul(t1))
		im := x2.Mul(t0).Add(x1.Mul(t1))
		bflyStoreCpx8AMD64(outF[2*i:], re, im)
	}
	archsimd.ClearAVXUpperBits()
	for ; i+4 <= n4; i += 4 {
		s0 := archsimd.LoadFloat32x4Array((*[4]float32)(spectrum[2*i:]))
		s1 := archsimd.LoadFloat32x4Array((*[4]float32)(spectrum[2*i+4:]))
		x1 := evenLanes4(s0, s1)
		b := n2 - 8 - 2*i
		g0 := archsimd.LoadFloat32x4Array((*[4]float32)(spectrum[b:]))
		g1 := archsimd.LoadFloat32x4Array((*[4]float32)(spectrum[b+4:]))
		x2 := reverseLanes4(oddLanes4(g0, g1))
		t0 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:]))
		t1 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:]))
		re := x1.Mul(t0).Sub(x2.Mul(t1))
		im := x2.Mul(t0).Add(x1.Mul(t1))
		storeInterleaved4(outF[2*i:], re, im)
	}
	for ; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		outF[2*i] = noFMA32Sub(noFMA32Mul(x1, t0), noFMA32Mul(x2, t1))
		outF[2*i+1] = noFMA32Add(noFMA32Mul(x2, t0), noFMA32Mul(x1, t1))
	}
}

//go:noinline
func imdctPreRotateNoFMAAVX(fftIn []float32, spectrum []float32, trig []float32, n2, n4 int) {
	spectrum = spectrum[:max(n2, 2*n4)]
	trig = trig[:n2]
	outF := fftIn[:2*n4]

	i := 0
	for ; i+4 <= n4; i += 4 {
		s0 := archsimd.LoadFloat32x4Array((*[4]float32)(spectrum[2*i:]))
		s1 := archsimd.LoadFloat32x4Array((*[4]float32)(spectrum[2*i+4:]))
		x1 := evenLanes4(s0, s1)
		b := n2 - 8 - 2*i
		g0 := archsimd.LoadFloat32x4Array((*[4]float32)(spectrum[b:]))
		g1 := archsimd.LoadFloat32x4Array((*[4]float32)(spectrum[b+4:]))
		x2 := reverseLanes4(oddLanes4(g0, g1))
		t0 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:]))
		t1 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:]))
		re := x1.Mul(t0).Sub(x2.Mul(t1))
		im := x2.Mul(t0).Add(x1.Mul(t1))
		storeInterleaved4(outF[2*i:], re, im)
	}
	for ; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		outF[2*i] = noFMA32Sub(noFMA32Mul(x1, t0), noFMA32Mul(x2, t1))
		outF[2*i+1] = noFMA32Add(noFMA32Mul(x2, t0), noFMA32Mul(x1, t1))
	}
}
