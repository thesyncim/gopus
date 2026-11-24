//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var imdctTDACUsesAVX = archsimd.X86.AVX()
var imdctTDACUsesAVX2 = archsimd.X86.AVX2() && (!mdctUseFMALikeMixEnabled || archsimd.X86.FMA())

// imdctTDACWindow is the IMDCT TDAC overlap-add windowing of
// clt_mdct_backward_c() on eight lanes (AVX2) or four lanes (AVX1). Per step
// it computes
//
//	out[yp]    = round(x2*w2) - round(x1*w1)
//	out[xpOut] = round(x2*w1) + round(x1*w2)
//
// with separately rounded products through AMD64 v2 and a fused first source
// product on AMD64 v3, so each lane follows imdctTDACWindowScalar's target
// arithmetic.
func imdctTDACWindow(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int) {
	if imdctTDACUsesAVX2 && count >= 8 {
		imdctTDACWindowAVX2(out, xsrc, window, yOut0, xOut0, xSrc0, wBwd0, count)
		return
	}
	if !imdctTDACUsesAVX || count < 4 {
		imdctTDACWindowScalar(out, xsrc, window, yOut0, xOut0, xSrc0, wBwd0, count)
		return
	}
	imdctTDACWindowAVX(out, xsrc, window, yOut0, xOut0, xSrc0, wBwd0, count)
}

//go:noinline
func imdctTDACWindowAVX2(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int) {
	_ = out[yOut0]
	_ = out[yOut0+count-1]
	_ = out[xOut0]
	_ = out[xOut0-count+1]
	_ = xsrc[xSrc0]
	_ = xsrc[xSrc0-count+1]
	_ = window[count-1]
	_ = window[wBwd0]
	_ = window[wBwd0-count+1]
	i := 0
	for ; i+8 <= count; i += 8 {
		x1 := reverseF32x8(archsimd.LoadFloat32x8Array((*[8]float32)(xsrc[xSrc0-i-7:])))
		x2 := archsimd.LoadFloat32x8Array((*[8]float32)(out[yOut0+i:]))
		w1 := archsimd.LoadFloat32x8Array((*[8]float32)(window[i:]))
		w2 := reverseF32x8(archsimd.LoadFloat32x8Array((*[8]float32)(window[wBwd0-i-7:])))
		yr := x2.Mul(w2).Sub(x1.Mul(w1))
		xr := x2.Mul(w1).Add(x1.Mul(w2))
		if mdctUseFMALikeMixEnabled {
			yr = x2.MulAdd(w2, x1.Mul(w1).Neg())
			xr = x2.MulAdd(w1, x1.Mul(w2))
		}
		yr.StoreArray((*[8]float32)(out[yOut0+i:]))
		reverseF32x8(xr).StoreArray((*[8]float32)(out[xOut0-i-7:]))
	}
	archsimd.ClearAVXUpperBits()
	if i+4 <= count {
		x1 := reverseF32x4(archsimd.LoadFloat32x4Array((*[4]float32)(xsrc[xSrc0-i-3:])))
		x2 := archsimd.LoadFloat32x4Array((*[4]float32)(out[yOut0+i:]))
		w1 := archsimd.LoadFloat32x4Array((*[4]float32)(window[i:]))
		w2 := reverseF32x4(archsimd.LoadFloat32x4Array((*[4]float32)(window[wBwd0-i-3:])))
		yr := x2.Mul(w2).Sub(x1.Mul(w1))
		xr := x2.Mul(w1).Add(x1.Mul(w2))
		if mdctUseFMALikeMixEnabled {
			yr = x2.MulAdd(w2, x1.Mul(w1).Neg())
			xr = x2.MulAdd(w1, x1.Mul(w2))
		}
		yr.StoreArray((*[4]float32)(out[yOut0+i:]))
		reverseF32x4(xr).StoreArray((*[4]float32)(out[xOut0-i-3:]))
		i += 4
	}
	for ; i < count; i++ {
		x1 := xsrc[xSrc0-i]
		x2 := out[yOut0+i]
		w1 := window[i]
		w2 := window[wBwd0-i]
		out[yOut0+i] = mdctMulSubMix(x2, x1, w2, w1)
		out[xOut0-i] = mdctMulAddMix(x2, x1, w1, w2)
	}
}

//go:noinline
func imdctTDACWindowAVX(out, xsrc, window []float32, yOut0, xOut0, xSrc0, wBwd0, count int) {
	_ = out[yOut0]
	_ = out[yOut0+count-1]
	_ = out[xOut0]
	_ = out[xOut0-count+1]
	_ = xsrc[xSrc0]
	_ = xsrc[xSrc0-count+1]
	_ = window[count-1]
	_ = window[wBwd0]
	_ = window[wBwd0-count+1]
	i := 0
	for ; i+4 <= count; i += 4 {
		x1 := reverseF32x4(archsimd.LoadFloat32x4Array((*[4]float32)(xsrc[xSrc0-i-3:])))
		x2 := archsimd.LoadFloat32x4Array((*[4]float32)(out[yOut0+i:]))
		w1 := archsimd.LoadFloat32x4Array((*[4]float32)(window[i:]))
		w2 := reverseF32x4(archsimd.LoadFloat32x4Array((*[4]float32)(window[wBwd0-i-3:])))
		yr := x2.Mul(w2).Sub(x1.Mul(w1))
		xr := x2.Mul(w1).Add(x1.Mul(w2))
		if mdctUseFMALikeMixEnabled {
			// The libopus SIMD TDAC path fuses the first source product and
			// rounds the second product before the add/subtract.
			yr = x2.MulAdd(w2, x1.Mul(w1).Neg())
			xr = x2.MulAdd(w1, x1.Mul(w2))
		}
		yr.StoreArray((*[4]float32)(out[yOut0+i:]))
		reverseF32x4(xr).StoreArray((*[4]float32)(out[xOut0-i-3:]))
	}
	for ; i < count; i++ {
		x1 := xsrc[xSrc0-i]
		x2 := out[yOut0+i]
		w1 := window[i]
		w2 := window[wBwd0-i]
		out[yOut0+i] = mdctMulSubMix(x2, x1, w2, w1)
		out[xOut0-i] = mdctMulAddMix(x2, x1, w1, w2)
	}
}

// reverseF32x4 returns v's lanes in reverse order.
func reverseF32x4(v archsimd.Float32x4) archsimd.Float32x4 {
	return v.ConcatPermuteScalars(3, 2, 1, 0, v)
}

// reverseF32x8 returns v's eight lanes in reverse order.
func reverseF32x8(v archsimd.Float32x8) archsimd.Float32x8 {
	swapped := v.ConcatPermute128Scalars(1, 0, v)
	return swapped.ConcatPermuteScalarsGrouped(3, 2, 5, 4, swapped)
}
