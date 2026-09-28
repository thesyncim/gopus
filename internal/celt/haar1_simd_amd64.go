//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"unsafe"

	"simd/archsimd"
)

// haar1Scale is haar1's QCONST32(.70710678f,31) in the float build.
const haar1Scale = float32(0.7071067811865476)

// haar1Stride1 runs haar1's stride-1 butterfly over n0 (even, odd) pairs,
// four pairs per step. Each lane scales both inputs before the sum and the
// difference, exactly as the scalar loop does.
func haar1Stride1(x []float32, n0 int) {
	if !archsimd.X86.AVX() {
		haar1StrideScalarAMD64(x, n0, 1)
		return
	}
	haar1Stride1AVX(x, n0)
}

//go:noinline
func haar1Stride1AVX(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	_ = x[2*n0-1]
	p := unsafe.Pointer(unsafe.SliceData(x))
	scale := broadcastF32x4Arch(haar1Scale)
	i := 0
	for ; i+4 <= n0; i += 4 {
		off := unsafe.Add(p, i*8)
		a := loadF32x4(off)
		b := loadF32x4(unsafe.Add(off, 16))
		even := a.ConcatPermuteScalars(0, 2, 4, 6, b).Mul(scale)
		odd := a.ConcatPermuteScalars(1, 3, 5, 7, b).Mul(scale)
		sum := even.Add(odd).ToBits()
		diff := even.Sub(odd).ToBits()
		storeF32x4(off, sum.InterleaveLo(diff).BitsToFloat32())
		storeF32x4(unsafe.Add(off, 16), sum.InterleaveHi(diff).BitsToFloat32())
	}
	for ; i < n0; i++ {
		t0 := noFMA32Mul(haar1Scale, x[2*i])
		t1 := noFMA32Mul(haar1Scale, x[2*i+1])
		x[2*i] = noFMA32Add(t0, t1)
		x[2*i+1] = noFMA32Sub(t0, t1)
	}
}

// haar1Stride2 runs haar1's stride-2 butterfly over n0 groups of four,
// pairing x[4j+i] with x[4j+2+i]; two groups per step.
func haar1Stride2(x []float32, n0 int) {
	if !archsimd.X86.AVX() {
		haar1StrideScalarAMD64(x, n0, 2)
		return
	}
	haar1Stride2AVX(x, n0)
}

//go:noinline
func haar1Stride2AVX(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	_ = x[4*n0-1]
	p := unsafe.Pointer(unsafe.SliceData(x))
	scale := broadcastF32x4Arch(haar1Scale)
	i := 0
	for ; i+2 <= n0; i += 2 {
		off := unsafe.Add(p, i*16)
		a := loadF32x4(off)
		b := loadF32x4(unsafe.Add(off, 16))
		lo := a.ConcatPermuteScalars(0, 1, 4, 5, b).Mul(scale)
		hi := a.ConcatPermuteScalars(2, 3, 6, 7, b).Mul(scale)
		sum := lo.Add(hi)
		diff := lo.Sub(hi)
		storeF32x4(off, sum.ConcatPermuteScalars(0, 1, 4, 5, diff))
		storeF32x4(unsafe.Add(off, 16), sum.ConcatPermuteScalars(2, 3, 6, 7, diff))
	}
	for ; i < n0; i++ {
		off := 4 * i
		t0 := noFMA32Mul(haar1Scale, x[off])
		t1 := noFMA32Mul(haar1Scale, x[off+1])
		t2 := noFMA32Mul(haar1Scale, x[off+2])
		t3 := noFMA32Mul(haar1Scale, x[off+3])
		x[off] = noFMA32Add(t0, t2)
		x[off+1] = noFMA32Add(t1, t3)
		x[off+2] = noFMA32Sub(t0, t2)
		x[off+3] = noFMA32Sub(t1, t3)
	}
}

// haar1Stride4 runs haar1's stride-4 butterfly over n0 groups of eight,
// pairing the low and high four lanes of each group.
func haar1Stride4(x []float32, n0 int) {
	if !archsimd.X86.AVX() {
		haar1StrideScalarAMD64(x, n0, 4)
		return
	}
	haar1Stride4AVX(x, n0)
}

//go:noinline
func haar1Stride4AVX(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	_ = x[8*n0-1]
	p := unsafe.Pointer(unsafe.SliceData(x))
	scale := broadcastF32x4Arch(haar1Scale)
	for i := range n0 {
		off := unsafe.Add(p, i*32)
		lo := loadF32x4(off).Mul(scale)
		hi := loadF32x4(unsafe.Add(off, 16)).Mul(scale)
		storeF32x4(off, lo.Add(hi))
		storeF32x4(unsafe.Add(off, 16), lo.Sub(hi))
	}
}

// haar1StrideScalarAMD64 keeps the vector kernels' per-pair operation order
// when the host does not support AVX.
func haar1StrideScalarAMD64(x []float32, n0, stride int) {
	for i := 0; i < n0; i++ {
		base := 2 * stride * i
		for j := 0; j < stride; j++ {
			haar1PairNorm(x, base+j, base+stride+j, haar1Scale)
		}
	}
}
