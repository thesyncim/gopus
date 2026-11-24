//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var expRotationUsesAVX2 = archsimd.X86.AVX2() && (!celtAMD64SIMDUseFMA || archsimd.X86.FMA())

// expRotation1Pass4 runs blocks 4-wide spreading-rotation steps starting at
// index first, advancing 4 indices per iteration in direction dir (+1
// ascending, -1 descending). When stride >= 8 and AVX2 is available, pairs of
// adjacent 4-wide blocks run on 8-wide AVX2 vectors.
//
//go:noinline
func expRotation1Pass4(x []float32, first, stride, blocks, dir int, c, s float32) {
	if blocks == 0 {
		return
	}
	_ = x[first+stride+3]
	_ = x[first+(blocks-1)*dir*4]
	_ = x[first+(blocks-1)*dir*4+stride+3]
	b := 0
	if expRotationUsesAVX2 && stride >= 8 && blocks >= 2 {
		cv8 := archsimd.BroadcastFloat32x8(c)
		sv8 := archsimd.BroadcastFloat32x8(s)
		msv8 := archsimd.BroadcastFloat32x8(-s)
		if dir > 0 {
			for ; b+2 <= blocks; b += 2 {
				i := first + b*4
				p1 := (*[8]float32)(x[i:])
				p2 := (*[8]float32)(x[i+stride:])
				x1 := archsimd.LoadFloat32x8Array(p1)
				x2 := archsimd.LoadFloat32x8Array(p2)
				x2p, x1p := expRotation1VectorPair8(x1, x2, cv8, sv8, msv8)
				x2p.StoreArray(p2)
				x1p.StoreArray(p1)
			}
		} else {
			for ; b+2 <= blocks; b += 2 {
				i := first - 4 - b*4
				p1 := (*[8]float32)(x[i:])
				p2 := (*[8]float32)(x[i+stride:])
				x1 := archsimd.LoadFloat32x8Array(p1)
				x2 := archsimd.LoadFloat32x8Array(p2)
				x2p, x1p := expRotation1VectorPair8(x1, x2, cv8, sv8, msv8)
				x2p.StoreArray(p2)
				x1p.StoreArray(p1)
			}
		}
		archsimd.ClearAVXUpperBits()
	}
	if b < blocks {
		cv := broadcastF32x4Arch(c)
		sv := broadcastF32x4Arch(s)
		msv := broadcastF32x4Arch(-s)
		for ; b < blocks; b++ {
			i := first + b*dir*4
			p1 := (*[4]float32)(x[i:])
			p2 := (*[4]float32)(x[i+stride:])
			x1 := archsimd.LoadFloat32x4Array(p1)
			x2 := archsimd.LoadFloat32x4Array(p2)
			x2p, x1p := expRotation1VectorPair(x1, x2, cv, sv, msv)
			x2p.StoreArray(p2)
			x1p.StoreArray(p1)
		}
	}
}
