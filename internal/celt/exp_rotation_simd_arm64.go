//go:build arm64 && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// expRotation1PassNeon runs blocks 4-wide spreading-rotation steps starting at
// index first, advancing 4 indices per iteration in direction dir (+1 ascending,
// -1 descending). Per index i with x1=x[i], x2=x[i+stride]:
//
//	x[i+stride] = c*x2 + round(s*x1)
//	x[i]        = c*x1 + round(-s*x2)
//
// The two tap products round as plain Muls and the cross terms are fused
// MulAdds, matching expRotationMac32 and the hand asm bit-for-bit. stride >= 4
// keeps the four lanes of a block independent; raw-pointer loads (loadF32x4)
// skip the per-lane bounds check.
func expRotation1PassNeon(x []float32, first, stride, blocks, dir int, c, s float32) {
	if blocks == 0 {
		return
	}
	cv := archsimd.BroadcastFloat32x4(c)
	sv := archsimd.BroadcastFloat32x4(s)
	msv := sv.Neg()
	base := unsafe.Pointer(unsafe.SliceData(x))
	p1 := unsafe.Add(base, first*4)
	p2 := unsafe.Add(base, (first+stride)*4)
	step := dir * 16
	for b := 0; b < blocks; b++ {
		x1 := loadF32x4(p1)
		x2 := loadF32x4(p2)
		x2p := x2.MulAdd(cv, x1.Mul(sv))
		x1p := x1.MulAdd(cv, x2.Mul(msv))
		storeF32x4(p2, x2p)
		storeF32x4(p1, x1p)
		p1 = unsafe.Add(p1, step)
		p2 = unsafe.Add(p2, step)
	}
}
