//go:build (amd64 || arm64) && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// prefilterDualInnerProdArchSIMD computes sum1=<x,y1> and sum2=<x,y2> with two
// 4-lane fused-multiply-add accumulators (archsimd MulAdd → FMLA/VFMADD), loading
// through raw pointers (loadF32x4) to drop the per-load slice bounds check. Lane L
// of each accumulator sums elements L, L+4, L+8, … and the reductions are
// (a0+a2)+(a1+a3) with a scalar fused tail, matching the scalar reference
// bit-for-bit. Four lanes is mandatory; MulAdd needs the FMA feature, so callers
// gate amd64 on archsimd.X86.FMA() (arm64 NEON always has FMLA).
func prefilterDualInnerProdArchSIMD(x, y1, y2 []float32, length int) (float32, float32) {
	acc1 := archsimd.BroadcastFloat32x4(0)
	acc2 := archsimd.BroadcastFloat32x4(0)
	xp := unsafe.Pointer(unsafe.SliceData(x))
	y1p := unsafe.Pointer(unsafe.SliceData(y1))
	y2p := unsafe.Pointer(unsafe.SliceData(y2))
	i := 0
	for ; i+8 <= length; i += 8 {
		xv0 := loadF32x4(xp)
		acc1 = xv0.MulAdd(loadF32x4(y1p), acc1)
		acc2 = xv0.MulAdd(loadF32x4(y2p), acc2)
		xv1 := loadF32x4(unsafe.Add(xp, 16))
		acc1 = xv1.MulAdd(loadF32x4(unsafe.Add(y1p, 16)), acc1)
		acc2 = xv1.MulAdd(loadF32x4(unsafe.Add(y2p, 16)), acc2)
		xp = unsafe.Add(xp, 32)
		y1p = unsafe.Add(y1p, 32)
		y2p = unsafe.Add(y2p, 32)
	}
	for ; i+4 <= length; i += 4 {
		xv := loadF32x4(xp)
		acc1 = xv.MulAdd(loadF32x4(y1p), acc1)
		acc2 = xv.MulAdd(loadF32x4(y2p), acc2)
		xp = unsafe.Add(xp, 16)
		y1p = unsafe.Add(y1p, 16)
		y2p = unsafe.Add(y2p, 16)
	}
	sum1 := round32(round32(acc1.GetElem(0)+acc1.GetElem(2)) + round32(acc1.GetElem(1)+acc1.GetElem(3)))
	sum2 := round32(round32(acc2.GetElem(0)+acc2.GetElem(2)) + round32(acc2.GetElem(1)+acc2.GetElem(3)))
	for ; i < length; i++ {
		xv := *(*float32)(xp)
		sum1 = mdctFMA32(xv, *(*float32)(y1p), sum1)
		sum2 = mdctFMA32(xv, *(*float32)(y2p), sum2)
		xp = unsafe.Add(xp, 4)
		y1p = unsafe.Add(y1p, 4)
		y2p = unsafe.Add(y2p, 4)
	}
	return sum1, sum2
}
