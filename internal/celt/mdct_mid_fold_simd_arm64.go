//go:build arm64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// mdctUseNeonMidFold enables the archsimd middle-fold/store kernel.
const mdctUseNeonMidFold = true

// mdctMidFoldStoreNeon writes blocks*4 outputs of the forward-MDCT middle fold.
// Per output j: re=samples[xp2-2j] (descending), im=samples[xp1+2j] (ascending),
//
//	yr = (re*trig[i0+j] - round(im*trig[n4+i0+j])) * preScale
//	yi = (im*trig[i0+j] + round(re*trig[n4+i0+j])) * preScale
//	dst[bitrev[i0+j]] = {yr, yi}
//
// The compute is vectorized four lanes at a time (ConcatEven deinterleave, the
// descending re reversed with reverse4, fused MulAdd, scaling Mul), matching
// mdctStoreDirectStageFMALike bit-for-bit; only the bit-reversed store is
// scalar, exactly as the asm scatters it.
func mdctMidFoldStoreNeon(dst []kissCpx, bitrev []int, samples []float32, trig []float32, i0, n4, xp1, xp2, blocks int, preScale float32) {
	const intBytes = int(unsafe.Sizeof(int(0)))
	if blocks == 0 {
		return
	}
	_ = samples[xp1+8*blocks-1]
	_ = samples[xp2+1]
	_ = samples[xp2-6-8*(blocks-1)]
	_ = trig[n4+i0+4*blocks-1]
	_ = bitrev[i0+4*blocks-1]
	pv := archsimd.BroadcastFloat32x4(preScale)
	sp := unsafe.Pointer(unsafe.SliceData(samples))
	tp := unsafe.Pointer(unsafe.SliceData(trig))
	// The last-element check above validates every bit-reversal lane read here.
	bitrevp := unsafe.Pointer(&bitrev[i0])
	for b := 0; b < blocks; b++ {
		x1 := xp1 + 8*b
		x2 := xp2 - 8*b
		im := loadF32x4(unsafe.Add(sp, x1*4)).ToBits().
			ConcatEven(loadF32x4(unsafe.Add(sp, (x1+4)*4)).ToBits()).BitsToFloat32()
		re := reverse4(loadF32x4(unsafe.Add(sp, (x2-6)*4)).ToBits().
			ConcatEven(loadF32x4(unsafe.Add(sp, (x2-2)*4)).ToBits()).BitsToFloat32())
		t0 := loadF32x4(unsafe.Add(tp, (i0+4*b)*4))
		t1 := loadF32x4(unsafe.Add(tp, (n4+i0+4*b)*4))
		yr := re.MulAdd(t0, im.Mul(t1).Neg()).Mul(pv)
		yi := im.MulAdd(t0, re.Mul(t1)).Mul(pv)
		groupBitrev := unsafe.Add(bitrevp, b*4*intBytes)
		rev0 := *(*int)(groupBitrev)
		rev1 := *(*int)(unsafe.Add(groupBitrev, intBytes))
		rev2 := *(*int)(unsafe.Add(groupBitrev, 2*intBytes))
		rev3 := *(*int)(unsafe.Add(groupBitrev, 3*intBytes))
		dst[rev0] = kissCpx{r: yr.GetElem(0), i: yi.GetElem(0)}
		dst[rev1] = kissCpx{r: yr.GetElem(1), i: yi.GetElem(1)}
		dst[rev2] = kissCpx{r: yr.GetElem(2), i: yi.GetElem(2)}
		dst[rev3] = kissCpx{r: yr.GetElem(3), i: yi.GetElem(3)}
	}
}
