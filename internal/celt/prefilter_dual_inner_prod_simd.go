//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// prefilterDualInnerProdArchSIMD computes sum1=<x,y1> and sum2=<x,y2> with two
// 4-lane fused-multiply-add accumulators (archsimd MulAdd → FMLA). Lane L
// of each accumulator sums elements L, L+4, L+8, … and the reductions are
// (a0+a2)+(a1+a3) with a scalar fused tail, matching the scalar reference
// bit-for-bit. Four lanes are mandatory; the arm64 build uses this implementation
// because NEON provides FMLA.
func prefilterDualInnerProdArchSIMD(x, y1, y2 []float32, length int) (float32, float32) {
	if length <= 0 {
		return 0, 0
	}
	x = x[:length]
	y1 = y1[:length]
	y2 = y2[:length]
	acc1 := broadcastF32x4Arch(0)
	acc2 := broadcastF32x4Arch(0)
	i := 0
	for ; i+8 <= length; i += 8 {
		xv0 := archsimd.LoadFloat32x4Array((*[4]float32)(x[i:]))
		acc1 = xv0.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(y1[i:])), acc1)
		acc2 = xv0.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(y2[i:])), acc2)
		xv1 := archsimd.LoadFloat32x4Array((*[4]float32)(x[i+4:]))
		acc1 = xv1.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(y1[i+4:])), acc1)
		acc2 = xv1.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(y2[i+4:])), acc2)
	}
	for ; i+4 <= length; i += 4 {
		xv := archsimd.LoadFloat32x4Array((*[4]float32)(x[i:]))
		acc1 = xv.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(y1[i:])), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x4Array((*[4]float32)(y2[i:])), acc2)
	}
	sum1 := round32(round32(acc1.GetElem(0)+acc1.GetElem(2)) + round32(acc1.GetElem(1)+acc1.GetElem(3)))
	sum2 := round32(round32(acc2.GetElem(0)+acc2.GetElem(2)) + round32(acc2.GetElem(1)+acc2.GetElem(3)))
	for ; i < length; i++ {
		xv := x[i]
		sum1 = opusmath.FMA32(xv, y1[i], sum1)
		sum2 = opusmath.FMA32(xv, y2[i], sum2)
	}
	return sum1, sum2
}
