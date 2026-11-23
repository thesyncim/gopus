//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// mdctFoldEvenAt deinterleaves the even lanes of one eight-sample block.
func mdctFoldEvenAt(s []float32) archsimd.Float32x4 {
	return archsimd.LoadFloat32x4Array((*[4]float32)(s)).ToBits().
		ConcatEven(archsimd.LoadFloat32x4Array((*[4]float32)(s[4:])).ToBits()).BitsToFloat32()
}

// mdctFoldEvenDescAt reads base+6, base+4, base+2, base without crossing the
// high end of its backing slice: s starts one sample before base.
func mdctFoldEvenDescAt(s []float32) archsimd.Float32x4 {
	lo := archsimd.LoadFloat32x4Array((*[4]float32)(s)).ToBits()
	hi := archsimd.LoadFloat32x4Array((*[4]float32)(s[4:])).ToBits()
	return reverse4(lo.ConcatOdd(hi).BitsToFloat32())
}

// mdctFoldStore applies the shared twiddle/scale/bit-reversed-scatter tail to a
// computed (re, im) block, matching mdctStoreDirectStageFMALike bit-for-bit:
// yr = (re*t0 - round(im*t1))*preScale, yi = (im*t0 + round(re*t1))*preScale,
// dst[bitrev[j+lane]] = {yr, yi}.
func mdctFoldStore(dst []kissCpx, bitrev []int, t0, t1 []float32, j int, re, im, pv archsimd.Float32x4) {
	tv0 := archsimd.LoadFloat32x4Array((*[4]float32)(t0))
	tv1 := archsimd.LoadFloat32x4Array((*[4]float32)(t1))
	yr := re.MulAdd(tv0, im.Mul(tv1).Neg()).Mul(pv)
	yi := im.MulAdd(tv0, re.Mul(tv1)).Mul(pv)
	br := bitrev[j : j+4]
	dst[br[0]] = kissCpx{r: yr.GetElem(0), i: yi.GetElem(0)}
	dst[br[1]] = kissCpx{r: yr.GetElem(1), i: yi.GetElem(1)}
	dst[br[2]] = kissCpx{r: yr.GetElem(2), i: yi.GetElem(2)}
	dst[br[3]] = kissCpx{r: yr.GetElem(3), i: yi.GetElem(3)}
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
	done := 4 * blocks
	_ = samples[xp1]
	_ = samples[xp1+n2+2*done-1]
	_ = samples[xp2]
	_ = samples[xp2-n2-2*done+1]
	_ = window[wp1]
	_ = window[wp1+2*done-1]
	_ = window[wp2]
	_ = window[wp2-2*done+1]
	_ = trig[n4+i0+done-1]
	_ = bitrev[i0+done-1]
	pv := archsimd.BroadcastFloat32x4(preScale)
	aOff := xp1 + n2
	a2Off := xp1
	wcOff := wp1
	bpOff := xp2 - 7
	b2Off := xp2 - n2 - 7
	wdOff := wp2 - 7
	t0Off := i0
	t1Off := n4 + i0
	for b := 0; b < blocks; b++ {
		A := mdctFoldEvenAt(samples[aOff:])
		A2 := mdctFoldEvenAt(samples[a2Off:])
		wC := mdctFoldEvenAt(window[wcOff:])
		B := mdctFoldEvenDescAt(samples[bpOff:])
		B2 := mdctFoldEvenDescAt(samples[b2Off:])
		wD := mdctFoldEvenDescAt(window[wdOff:])
		re := A.MulAdd(wD, B.Mul(wC))
		im := A2.MulAdd(wC, B2.Mul(wD).Neg())
		mdctFoldStore(dst, bitrev, trig[t0Off:], trig[t1Off:], i0+4*b, re, im, pv)
		aOff += 8
		a2Off += 8
		wcOff += 8
		bpOff -= 8
		b2Off -= 8
		wdOff -= 8
		t0Off += 4
		t1Off += 4
	}
}

// mdctFold3StoreNeon is the archsimd trailing windowed fold. Per output j:
// re = fma(-A3, wC, round(B*wD)), im = fma(A2, wD, round(B4*wC)), with
// A3=s[xp1-n2+2j], A2=s[xp1+2j], B=s[xp2-2j], B4=s[xp2+n2-2j], wC=w[wp1+2j],
// wD=w[wp2-2j]. Both fuse the first product as clang -ffp-contract=on does for
// clt_mdct_forward_c(), matching mdctNegMulAddMixEncode/mdctMulAddMixEncode
// bit-for-bit.
func mdctFold3StoreNeon(dst []kissCpx, bitrev []int, samples []float32, window []float32, trig []float32, i0, n4, n2, xp1, xp2, wp1, wp2, blocks int, preScale float32) {
	if blocks == 0 {
		return
	}
	done := 4 * blocks
	_ = samples[xp1-n2]
	_ = samples[xp1+2*done-1]
	_ = samples[xp2+n2]
	_ = samples[xp2-2*done+1]
	_ = window[wp1]
	_ = window[wp1+2*done-1]
	_ = window[wp2]
	_ = window[wp2-2*done+1]
	_ = trig[n4+i0+done-1]
	_ = bitrev[i0+done-1]
	pv := archsimd.BroadcastFloat32x4(preScale)
	a3Off := xp1 - n2
	a2Off := xp1
	wcOff := wp1
	bpOff := xp2 - 7
	b4Off := xp2 + n2 - 7
	wdOff := wp2 - 7
	t0Off := i0
	t1Off := n4 + i0
	for b := 0; b < blocks; b++ {
		A3 := mdctFoldEvenAt(samples[a3Off:])
		A2 := mdctFoldEvenAt(samples[a2Off:])
		wC := mdctFoldEvenAt(window[wcOff:])
		B := mdctFoldEvenDescAt(samples[bpOff:])
		B4 := mdctFoldEvenDescAt(samples[b4Off:])
		wD := mdctFoldEvenDescAt(window[wdOff:])
		re := A3.Neg().MulAdd(wC, B.Mul(wD))
		im := A2.MulAdd(wD, B4.Mul(wC))
		mdctFoldStore(dst, bitrev, trig[t0Off:], trig[t1Off:], i0+4*b, re, im, pv)
		a3Off += 8
		a2Off += 8
		wcOff += 8
		bpOff -= 8
		b4Off -= 8
		wdOff -= 8
		t0Off += 4
		t1Off += 4
	}
}
