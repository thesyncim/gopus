//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// expRotation1Pass4 runs blocks 4-wide spreading-rotation steps starting at
// index first, advancing 4 indices per iteration in direction dir (+1
// ascending, -1 descending). Per index i with x1=x[i], x2=x[i+stride]:
//
//	x[i+stride] = round(c*x2) + round(s*x1)
//	x[i]        = round(c*x1) + round(-s*x2)
//
// which is exp_rotation1's MAC16_16(MULT16_16(c, x2), s, x1) without
// contraction, as gcc compiles it for x86 and as expRotationMac32 evaluates
// it on amd64. stride >= 4 keeps the four lanes of a block independent.
func expRotation1Pass4(x []float32, first, stride, blocks, dir int, c, s float32) {
	if blocks == 0 {
		return
	}
	_ = x[first+stride+3]
	_ = x[first+(blocks-1)*dir*4]
	_ = x[first+(blocks-1)*dir*4+stride+3]
	cv := archsimd.BroadcastFloat32x4(c)
	sv := archsimd.BroadcastFloat32x4(s)
	msv := archsimd.BroadcastFloat32x4(-s)
	base := unsafe.Pointer(unsafe.SliceData(x))
	for b := range blocks {
		p1 := unsafe.Add(base, (first+b*dir*4)*4)
		p2 := unsafe.Add(p1, stride*4)
		x1 := loadF32x4(p1)
		x2 := loadF32x4(p2)
		storeF32x4(p2, x2.Mul(cv).Add(x1.Mul(sv)))
		storeF32x4(p1, x1.Mul(cv).Add(x2.Mul(msv)))
	}
}
