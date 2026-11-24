//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// expRotation1Pass4 runs blocks 4-wide spreading-rotation steps starting at
// index first, advancing 4 indices per iteration in direction dir (+1 ascending,
// -1 descending). Per index i with x1=x[i], x2=x[i+stride]:
//
//	x[i+stride] = c*x2 + round(s*x1)
//	x[i]        = c*x1 + round(-s*x2)
//
// The two tap products round as plain Muls and the cross terms are fused
// MulAdds, matching expRotationMac32 bit-for-bit. stride >= 4 keeps the four
// lanes of a block independent.
func expRotation1Pass4(x []float32, first, stride, blocks, dir int, c, s float32) {
	if blocks <= 0 {
		return
	}
	_ = x[first]
	_ = x[first+stride+3]
	_ = x[first+(blocks-1)*dir*4]
	_ = x[first+(blocks-1)*dir*4+stride+3]
	cv := archsimd.BroadcastFloat32x4(c)
	sv := archsimd.BroadcastFloat32x4(s)
	msv := sv.Neg()
	for b := 0; b < blocks; b++ {
		i := first + b*dir*4
		p1 := (*[4]float32)(x[i:])
		p2 := (*[4]float32)(x[i+stride:])
		x1 := archsimd.LoadFloat32x4Array(p1)
		x2 := archsimd.LoadFloat32x4Array(p2)
		x2p := x2.MulAdd(cv, x1.Mul(sv))
		x1p := x1.MulAdd(cv, x2.Mul(msv))
		x2p.StoreArray(p2)
		x1p.StoreArray(p1)
	}
}
