//go:build arm64 && goexperiment.simd && !purego && gopus_reverse64

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// reverse4 fully reverses a Float32x4 ([a,b,c,d] -> [d,c,b,a]). Reverse64 (the
// arm64 VREV64 op added in this toolchain) swaps the two 32-bit elements within
// each 64-bit lane ([a,b,c,d]->[b,a,d,c]); a byte-level VEXT half-swap then
// exchanges the two 64-bit halves.
func reverse4(v archsimd.Float32x4) archsimd.Float32x4 {
	b := v.Reverse64().ToBits().ReshapeToUint8s()
	return b.ConcatShiftBytesRight(b, 8).ReshapeToUint32s().BitsToFloat32()
}

// imdctPreRotateFMA32Kiss is the archsimd IMDCT pre-rotation. Per output i it
// computes the complex rotation
//
//	x1 = spectrum[2i] (even, ascending); x2 = spectrum[n2-1-2i] (odd, descending)
//	fftIn[i] = (x1*t0 - x2*t1) + i(x2*t0 + x1*t1)
//
// vectorized four outputs at a time: x1 via ConcatEven, x2 via ConcatOdd + the
// reverse4 above (this is the kernel the missing lane-reverse blocked), the
// fused products via MulAdd, and the interleaved complex store via InterleaveLo/Hi.
// Each product is a single-rounding FMUL and each accumulate a fused FMLA, the same
// shape as the scalar mdctFMA32/noFMA32 reference, so the result is bit-exact.
func imdctPreRotateFMA32Kiss(fftIn []complex64, spectrum []float32, trig []float32, n2, n4 int) {
	if n4 <= 0 {
		return
	}
	_ = spectrum[n2-1]
	_ = trig[n2-1]
	_ = fftIn[n4-1]
	sp := unsafe.Pointer(unsafe.SliceData(spectrum))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	fp := unsafe.Pointer(unsafe.SliceData(fftIn))
	i := 0
	for ; i+4 <= n4; i += 4 {
		x1 := loadF32x4(unsafe.Add(sp, (2*i)*4)).ToBits().
			ConcatEven(loadF32x4(unsafe.Add(sp, (2*i+4)*4)).ToBits()).BitsToFloat32()
		base := n2 - 8 - 2*i
		oddAsc := loadF32x4(unsafe.Add(sp, base*4)).ToBits().
			ConcatOdd(loadF32x4(unsafe.Add(sp, (base+4)*4)).ToBits()).BitsToFloat32()
		x2 := reverse4(oddAsc)
		t0 := loadF32x4(unsafe.Add(tp, i*4))
		t1 := loadF32x4(unsafe.Add(tp, (n4+i)*4))
		re := x1.MulAdd(t0, x2.Mul(t1).Neg())
		im := x2.MulAdd(t0, x1.Mul(t1))
		storeF32x4(unsafe.Add(fp, i*8), re.ToBits().InterleaveLo(im.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(fp, i*8+16), re.ToBits().InterleaveHi(im.ToBits()).BitsToFloat32())
	}
	for ; i < n4; i++ {
		x1 := spectrum[2*i]
		x2 := spectrum[n2-1-2*i]
		t0 := trig[i]
		t1 := trig[n4+i]
		fftIn[i] = complex(
			mdctFMA32(x1, t0, -noFMA32Mul(x2, t1)),
			mdctFMA32(x2, t0, noFMA32Mul(x1, t1)),
		)
	}
}
