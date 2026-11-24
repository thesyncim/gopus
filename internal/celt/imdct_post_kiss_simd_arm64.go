//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// imdctPostRotateF32FromKiss is the archsimd IMDCT post-rotation, reading kissCpx
// scratch directly. Per iteration i (k = n4-1-i), with re=fft[i].i, im=fft[i].r and
// re2=fft[k].i, im2=fft[k].r:
//
//	buf[2i]      = re*trig[i]      + round(im*trig[n4+i])
//	buf[n2-1-2i] = re*trig[n4+i]   - round(im*trig[i])
//	buf[n2-2-2i] = re2*trig[n4-1-i] + round(im2*trig[n2-1-i])
//	buf[2i+1]    = re2*trig[n2-1-i] - round(im2*trig[n4-1-i])
//
// The forward four come from a ConcatEven/Odd deinterleave of fft; the backward
// four walk down from fft[n4-1] and reverse4 so the descending lanes line up.
// Each product is a single-round Mul and each accumulate a fused MulAdd, matching
// mdctMulAddMix/mdctMulSubMix bit-for-bit; a scalar loop finishes limit%4.
func imdctPostRotateF32FromKiss(buf []float32, fft []float32, trig []float32, n2, n4 int) {
	if len(buf) < n2 || len(fft) < 2*n4 {
		return
	}
	limit := (n4 + 1) >> 1
	if limit <= 0 {
		return
	}
	buf = buf[:n2]
	ff := fft[:2*n4]
	trig = trig[:n2]

	i := 0
	for ; i+4 <= limit; i += 4 {
		// Forward fft[i..i+3]: deinterleave im=even(.r), re=odd(.i).
		f0 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*i:])).ToBits()
		f1 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*i+4:])).ToBits()
		im := f0.ConcatEven(f1).BitsToFloat32()
		re := f0.ConcatOdd(f1).BitsToFloat32()
		t0 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[i:]))
		t1 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4+i:]))
		yr := re.MulAdd(t0, im.Mul(t1))
		yi := re.MulAdd(t1, im.Mul(t0).Neg())

		// Backward fft[n4-4-i..n4-1-i]: deinterleave then reverse4 to descend.
		kbase := n4 - 4 - i
		g0 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*kbase:])).ToBits()
		g1 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[2*kbase+4:])).ToBits()
		im2 := reverse4(g0.ConcatEven(g1).BitsToFloat32())
		re2 := reverse4(g0.ConcatOdd(g1).BitsToFloat32())
		t0b := reverse4(archsimd.LoadFloat32x4Array((*[4]float32)(trig[n4-4-i:])))
		t1b := reverse4(archsimd.LoadFloat32x4Array((*[4]float32)(trig[n2-4-i:])))
		yr2 := re2.MulAdd(t0b, im2.Mul(t1b))
		yi2 := re2.MulAdd(t1b, im2.Mul(t0b).Neg())

		// Near pairs ascending: (buf[2i], buf[2i+1]) = (yr, yi2).
		yr.ToBits().InterleaveLo(yi2.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(buf[2*i:]))
		yr.ToBits().InterleaveHi(yi2.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(buf[2*i+4:]))

		// Far pairs descending: (buf[n2-2-2i], buf[n2-1-2i]) = (yr2, yi), reversed
		// so the four pairs land in memory order at the descending base.
		rYr2 := reverse4(yr2)
		rYi := reverse4(yi)
		fb := n2 - 8 - 2*i
		rYr2.ToBits().InterleaveLo(rYi.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(buf[fb:]))
		rYr2.ToBits().InterleaveHi(rYi.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(buf[fb+4:]))
	}
	for ; i < limit; i++ {
		k := n4 - 1 - i
		re := ff[2*i+1]
		im := ff[2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		buf[2*i] = mdctMulAddMix(re, im, t0, t1)
		buf[n2-1-2*i] = mdctMulSubMix(re, im, t1, t0)
		re2 := ff[2*k+1]
		im2 := ff[2*k]
		t0 = trig[n4-i-1]
		t1 = trig[n2-i-1]
		buf[n2-2-2*i] = mdctMulAddMix(re2, im2, t0, t1)
		buf[2*i+1] = mdctMulSubMix(re2, im2, t1, t0)
	}
}

// imdctPreRotateNoFMA is unreachable on arm64, whose float build takes the
// fused pre-rotation, and stays scalar.
func imdctPreRotateNoFMA(fftIn []float32, spectrum []float32, trig []float32, n2, n4 int) {
	imdctPreRotateNoFMAScalar(fftIn, spectrum, trig, n2, n4)
}
