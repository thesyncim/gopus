//go:build arm64 && goexperiment.simd && !purego && gopus_reverse64

package celt

import "unsafe"

// mdctUsePostTwiddleNeon enables the archsimd forward-MDCT post-twiddle.
const mdctUsePostTwiddleNeon = true

// mdctPostTwiddleNeon is the archsimd forward-MDCT post-twiddle. Per index i
// (re=fftStage[i].r, im=fftStage[i].i, t0=trig[i], t1=trig[n4+i]):
//
//	coeffs[2i]      = round(im*t1) - round(re*t0)
//	coeffs[n2-1-2i] = round(re*t1) + round(im*t0)
//
// Each block pairs a forward run i and its mirror j=n4-1-i so the two ends tile
// coeffs contiguously: the low write zips the forward yr with the reversed
// mirror yi, the high write zips the mirror yr with the reversed forward yi.
// Products are single-round Muls and the combines plain Sub/Add (no fusion),
// matching mdctMul and the scalar loop bit-for-bit. The caller does the n4%8
// middle scalarly, so this runs exactly pairBlocks blocks.
func mdctPostTwiddleNeon(coeffs []float32, fftStage []kissCpx, trig []float32, n2, n4, pairBlocks int) {
	if pairBlocks == 0 {
		return
	}
	_ = coeffs[n2-1]
	_ = trig[n2-1]
	_ = fftStage[n4-1]
	cp := unsafe.Pointer(unsafe.SliceData(coeffs))
	ffp := unsafe.Pointer(unsafe.SliceData(fftStage))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	for b := 0; b < pairBlocks; b++ {
		i := 4 * b
		// Forward fftStage[i..i+3]: re=even(.r), im=odd(.i).
		f0 := loadF32x4(unsafe.Add(ffp, (2*i)*4))
		f1 := loadF32x4(unsafe.Add(ffp, (2*i+4)*4))
		re := f0.ToBits().ConcatEven(f1.ToBits()).BitsToFloat32()
		im := f0.ToBits().ConcatOdd(f1.ToBits()).BitsToFloat32()
		t0 := loadF32x4(unsafe.Add(tp, i*4))
		t1 := loadF32x4(unsafe.Add(tp, (n4+i)*4))
		yrF := im.Mul(t1).Sub(re.Mul(t0))
		yiF := re.Mul(t1).Add(im.Mul(t0))

		// Mirror fftStage[n4-4-i..n4-1-i], ascending j.
		mbase := n4 - 4 - i
		g0 := loadF32x4(unsafe.Add(ffp, (2*mbase)*4))
		g1 := loadF32x4(unsafe.Add(ffp, (2*mbase+4)*4))
		reM := g0.ToBits().ConcatEven(g1.ToBits()).BitsToFloat32()
		imM := g0.ToBits().ConcatOdd(g1.ToBits()).BitsToFloat32()
		t0M := loadF32x4(unsafe.Add(tp, mbase*4))
		t1M := loadF32x4(unsafe.Add(tp, (n4+mbase)*4))
		yrM := imM.Mul(t1M).Sub(reM.Mul(t0M))
		yiM := reM.Mul(t1M).Add(imM.Mul(t0M))

		// Low region coeffs[2i..2i+7] = zip(yrF, reverse4(yiM)).
		rYiM := reverse4(yiM)
		storeF32x4(unsafe.Add(cp, (2*i)*4), yrF.ToBits().InterleaveLo(rYiM.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(cp, (2*i+4)*4), yrF.ToBits().InterleaveHi(rYiM.ToBits()).BitsToFloat32())

		// High region coeffs[n2-8-2i..n2-1-2i] = zip(yrM, reverse4(yiF)).
		rYiF := reverse4(yiF)
		hb := n2 - 8 - 2*i
		storeF32x4(unsafe.Add(cp, hb*4), yrM.ToBits().InterleaveLo(rYiF.ToBits()).BitsToFloat32())
		storeF32x4(unsafe.Add(cp, (hb+4)*4), yrM.ToBits().InterleaveHi(rYiF.ToBits()).BitsToFloat32())
	}
}
