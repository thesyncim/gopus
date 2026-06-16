//go:build arm64 && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// mdctFoldEven deinterleaves the even (ascending) lanes of samples[off..off+7].
func mdctFoldEven(p unsafe.Pointer, off int) archsimd.Float32x4 {
	return loadF32x4(unsafe.Add(p, off*4)).ToBits().
		ConcatEven(loadF32x4(unsafe.Add(p, (off+4)*4)).ToBits()).BitsToFloat32()
}

// mdctFoldEvenDesc deinterleaves even lanes of [base..base+7] then reverses, so
// the lanes descend: s[base+6], s[base+4], s[base+2], s[base].
func mdctFoldEvenDesc(p unsafe.Pointer, base int) archsimd.Float32x4 {
	return reverse4(mdctFoldEven(p, base))
}

// mdctFoldStore applies the shared twiddle/scale/bit-reversed-scatter tail to a
// computed (re, im) block, matching mdctStoreDirectStageFMALike bit-for-bit:
// yr = (re*t0 - round(im*t1))*preScale, yi = (im*t0 + round(re*t1))*preScale,
// dst[bitrev[i0+4b+lane]] = {yr, yi}.
func mdctFoldStore(dst []kissCpx, bitrev []int, tp unsafe.Pointer, i0, n4, b int, re, im, pv archsimd.Float32x4) {
	t0 := loadF32x4(unsafe.Add(tp, (i0+4*b)*4))
	t1 := loadF32x4(unsafe.Add(tp, (n4+i0+4*b)*4))
	yr := re.MulAdd(t0, im.Mul(t1).Neg()).Mul(pv)
	yi := im.MulAdd(t0, re.Mul(t1)).Mul(pv)
	var yrT, yiT [4]float32
	storeF32x4(unsafe.Pointer(&yrT[0]), yr)
	storeF32x4(unsafe.Pointer(&yiT[0]), yi)
	for lane := 0; lane < 4; lane++ {
		dst[bitrev[i0+4*b+lane]] = kissCpx{r: yrT[lane], i: yiT[lane]}
	}
}

// mdctFold1StoreNeon is the archsimd leading windowed fold of the forward MDCT.
// Per output j: re = A*wD + round(B*wC), im = A2*wC - round(B2*wD), with
// A=s[xp1+n2+2j], A2=s[xp1+2j], B=s[xp2-2j], B2=s[xp2-n2-2j], wC=w[wp1+2j],
// wD=w[wp2-2j]. Fused MulAdds + single-round Muls match the scalar
// mdctMulAddMixEncode/mdctMulSubMixEncode sequence bit-for-bit.
func mdctFold1StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32) {
	if blocks == 0 {
		return
	}
	_ = trig[n4+i0+4*blocks-1]
	_ = bitrev[i0+4*blocks-1]
	pv := archsimd.BroadcastFloat32x4(preScale)
	sp := unsafe.Pointer(unsafe.SliceData(samples))
	wp := unsafe.Pointer(unsafe.SliceData(window))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	for b := 0; b < blocks; b++ {
		o := 8 * b
		A := mdctFoldEven(sp, xp1+n2+o)
		A2 := mdctFoldEven(sp, xp1+o)
		wC := mdctFoldEven(wp, wp1+o)
		B := mdctFoldEvenDesc(sp, xp2-6-o)
		B2 := mdctFoldEvenDesc(sp, xp2-n2-6-o)
		wD := mdctFoldEvenDesc(wp, wp2-6-o)
		re := A.MulAdd(wD, B.Mul(wC))
		im := A2.MulAdd(wC, B2.Mul(wD).Neg())
		mdctFoldStore(dst, bitrev, tp, i0, n4, b, re, im, pv)
	}
}

// mdctFold3StoreNeon is the archsimd trailing windowed fold. Per output j:
// re = round(B*wD) - round(A3*wC), im = A2*wD + round(B4*wC), with
// A3=s[xp1-n2+2j], A2=s[xp1+2j], B=s[xp2-2j], B4=s[xp2+n2-2j], wC=w[wp1+2j],
// wD=w[wp2-2j]. re is two single-round Muls then a plain Sub (no fusion); im is
// a fused MulAdd, matching mdctMulSubMixAlt/mdctMulAddMixEncode bit-for-bit.
func mdctFold3StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32) {
	if blocks == 0 {
		return
	}
	_ = trig[n4+i0+4*blocks-1]
	_ = bitrev[i0+4*blocks-1]
	pv := archsimd.BroadcastFloat32x4(preScale)
	sp := unsafe.Pointer(unsafe.SliceData(samples))
	wp := unsafe.Pointer(unsafe.SliceData(window))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	for b := 0; b < blocks; b++ {
		o := 8 * b
		A3 := mdctFoldEven(sp, xp1-n2+o)
		A2 := mdctFoldEven(sp, xp1+o)
		wC := mdctFoldEven(wp, wp1+o)
		B := mdctFoldEvenDesc(sp, xp2-6-o)
		B4 := mdctFoldEvenDesc(sp, xp2+n2-6-o)
		wD := mdctFoldEvenDesc(wp, wp2-6-o)
		re := B.Mul(wD).Sub(A3.Mul(wC))
		im := A2.MulAdd(wD, B4.Mul(wC))
		mdctFoldStore(dst, bitrev, tp, i0, n4, b, re, im, pv)
	}
}
