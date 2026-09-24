//go:build arm64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// toneLPCCorr accumulates three correlations in four fused lanes and reduces
// adjacent pairs before the scalar tail, matching the arm64 float path.
func toneLPCCorr(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	_ = x[:cnt]
	_ = x[delay : delay+cnt]
	_ = x[delay2 : delay2+cnt]
	x0 := unsafe.Pointer(unsafe.SliceData(x))
	x1 := unsafe.Add(x0, uintptr(delay*4))
	x2 := unsafe.Add(x0, uintptr(delay2*4))
	zero := archsimd.BroadcastFloat32x4(0)
	a00, a01, a02 := zero, zero, zero
	i := 0
	for ; i+3 < cnt; i += 4 {
		xv := loadF32x4(unsafe.Add(x0, uintptr(i*4)))
		a00 = xv.MulAdd(xv, a00)
		a01 = xv.MulAdd(loadF32x4(unsafe.Add(x1, uintptr(i*4))), a01)
		a02 = xv.MulAdd(loadF32x4(unsafe.Add(x2, uintptr(i*4))), a02)
	}
	a00 = a00.ConcatAddPairs(a00)
	a00 = a00.ConcatAddPairs(a00)
	a01 = a01.ConcatAddPairs(a01)
	a01 = a01.ConcatAddPairs(a01)
	a02 = a02.ConcatAddPairs(a02)
	a02 = a02.ConcatAddPairs(a02)
	r00 = a00.GetElem(0)
	r01 = a01.GetElem(0)
	r02 = a02.GetElem(0)
	for ; i < cnt; i++ {
		xi := x[i]
		r00 = mdctFMA32(xi, xi, r00)
		r01 = mdctFMA32(xi, x[i+delay], r01)
		r02 = mdctFMA32(xi, x[i+delay2], r02)
	}
	return
}

func toneLPCCorrDelay1(x []float32, cnt int) (r00, r01, r02 float32) {
	return toneLPCCorr(x, cnt, 1, 2)
}
