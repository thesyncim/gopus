//go:build (amd64 || arm64) && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// innerProd8FMA32ArchSIMD accumulates the dot product x·y in four lanes with
// fused multiply-add (archsimd MulAdd → FMLA on arm64, VFMADD on amd64), loading
// through raw pointers (loadF32x4) to avoid the per-load slice bounds check. Lane
// L sums elements L, L+4, L+8, … exactly like the scalar reference's acc[L], and
// the horizontal reduction is the same (acc0+acc2)+(acc1+acc3) with a scalar fused
// tail, so the result is bit-identical. Four lanes is mandatory: a wider Float32x8
// accumulator would reduce a different partial-sum tree and diverge.
//
// MulAdd lowers to the FMA instruction unconditionally, so callers must ensure the
// feature is present — always on arm64 NEON, gated on archsimd.X86.FMA() on amd64.
func innerProd8FMA32ArchSIMD(x, y []float32, n int) float32 {
	acc := archsimd.BroadcastFloat32x4(0)
	if n <= 0 {
		return 0
	}
	_ = x[n-1]
	_ = y[n-1]
	xbase := unsafe.Pointer(unsafe.SliceData(x))
	ybase := unsafe.Pointer(unsafe.SliceData(y))
	i := 0
	for ; i+8 <= n; i += 8 {
		xp := unsafe.Add(xbase, i*4)
		yp := unsafe.Add(ybase, i*4)
		acc = loadF32x4(xp).MulAdd(loadF32x4(yp), acc)
		acc = loadF32x4(unsafe.Add(xp, 16)).MulAdd(loadF32x4(unsafe.Add(yp, 16)), acc)
	}
	for ; i+4 <= n; i += 4 {
		acc = loadF32x4(unsafe.Add(xbase, i*4)).MulAdd(loadF32x4(unsafe.Add(ybase, i*4)), acc)
	}
	sum0 := round32(acc.GetElem(0) + acc.GetElem(2))
	sum1 := round32(acc.GetElem(1) + acc.GetElem(3))
	sum := round32(sum0 + sum1)
	for ; i < n; i++ {
		sum = mdctFMA32(*(*float32)(unsafe.Add(xbase, i*4)), *(*float32)(unsafe.Add(ybase, i*4)), sum)
	}
	return sum
}
