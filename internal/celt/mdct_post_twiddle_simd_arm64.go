//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// mdctUsePostTwiddleNeon enables the archsimd forward-MDCT post-twiddle.
const mdctUsePostTwiddleNeon = true

// mdctPostTwiddleNeon is the archsimd forward-MDCT post-twiddle. Per index i
// (re=fftStage[i].r, im=fftStage[i].i, t0=trig[i], t1=trig[n4+i]):
//
//	coeffs[2i]      = fma(im, t1, -round(re*t0))
//	coeffs[n2-1-2i] = fma(re, t1, round(im*t0))
//
// Each block pairs a forward run i and its mirror j=n4-1-i so the two ends tile
// coeffs contiguously: the low write zips the forward yr with the reversed
// mirror yi, the high write zips the mirror yr with the reversed forward yi.
// The first product fuses into the combine as clang -ffp-contract=on does for
// clt_mdct_forward_c(), matching mdctMulSubMixEncode/mdctMulAddMixEncode and
// the scalar loop bit-for-bit. The caller does the n4%8
// middle scalarly, so this runs exactly pairBlocks blocks.
func mdctPostTwiddleNeon(coeffs []float32, fftStage []float32, trig []float32, n2, n4, pairBlocks int) {
	if pairBlocks <= 0 {
		return
	}
	if 8*pairBlocks > n4 {
		panic("celt: mdctPostTwiddleNeon pairBlocks out of range")
	}
	coeffs = coeffs[:n2]
	trig = trig[:n2]
	ff := fftStage[:2*n4]
	fwdFFT := 0
	mirFFT := 2 * (n4 - 4)
	fwdT0 := 0
	fwdT1 := n4
	mirT0 := n4 - 4
	mirT1 := 2*n4 - 4
	low := 0
	high := n2 - 8
	for b := 0; b < pairBlocks; b++ {
		// Forward fftStage[i..i+3]: re=even(.r), im=odd(.i).
		f0 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[fwdFFT:])).ToBits()
		f1 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[fwdFFT+4:])).ToBits()
		re := f0.ConcatEven(f1).BitsToFloat32()
		im := f0.ConcatOdd(f1).BitsToFloat32()
		t0 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[fwdT0:]))
		t1 := archsimd.LoadFloat32x4Array((*[4]float32)(trig[fwdT1:]))
		yrF := im.MulAdd(t1, re.Mul(t0).Neg())
		yiF := re.MulAdd(t1, im.Mul(t0))

		// Mirror fftStage[n4-4-i..n4-1-i], ascending j.
		g0 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[mirFFT:])).ToBits()
		g1 := archsimd.LoadFloat32x4Array((*[4]float32)(ff[mirFFT+4:])).ToBits()
		reM := g0.ConcatEven(g1).BitsToFloat32()
		imM := g0.ConcatOdd(g1).BitsToFloat32()
		t0M := archsimd.LoadFloat32x4Array((*[4]float32)(trig[mirT0:]))
		t1M := archsimd.LoadFloat32x4Array((*[4]float32)(trig[mirT1:]))
		yrM := imM.MulAdd(t1M, reM.Mul(t0M).Neg())
		yiM := reM.MulAdd(t1M, imM.Mul(t0M))

		// Low region coeffs[2i..2i+7] = zip(yrF, reverse4(yiM)).
		rYiM := reverse4(yiM)
		yrF.ToBits().InterleaveLo(rYiM.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(coeffs[low:]))
		yrF.ToBits().InterleaveHi(rYiM.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(coeffs[low+4:]))

		// High region coeffs[n2-8-2i..n2-1-2i] = zip(yrM, reverse4(yiF)).
		rYiF := reverse4(yiF)
		yrM.ToBits().InterleaveLo(rYiF.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(coeffs[high:]))
		yrM.ToBits().InterleaveHi(rYiF.ToBits()).BitsToFloat32().StoreArray((*[4]float32)(coeffs[high+4:]))

		fwdFFT += 8
		mirFFT -= 8
		fwdT0 += 4
		fwdT1 += 4
		mirT0 -= 4
		mirT1 -= 4
		low += 8
		high -= 8
	}
}
