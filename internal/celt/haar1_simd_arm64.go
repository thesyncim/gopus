//go:build arm64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"
)

const haarScale = float32(0.7071067811865476)

func haar1Stride1NEON(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	_ = x[2*n0-1]
	p := unsafe.Pointer(unsafe.SliceData(x))
	scale := archsimd.BroadcastFloat32x4(haarScale)
	i := 0
	for ; i+4 <= n0; i += 4 {
		off := unsafe.Add(p, i*8)
		a := loadF32x4(off).ToBits()
		b := loadF32x4(unsafe.Add(off, 16)).ToBits()
		even := a.ConcatEven(b).BitsToFloat32()
		odd := a.ConcatOdd(b).BitsToFloat32()
		sum := even.Add(odd).Mul(scale).ToBits()
		diff := even.Sub(odd).Mul(scale).ToBits()
		storeF32x4(off, sum.InterleaveLo(diff).BitsToFloat32())
		storeF32x4(unsafe.Add(off, 16), sum.InterleaveHi(diff).BitsToFloat32())
	}
	for ; i < n0; i++ {
		a, b := x[2*i], x[2*i+1]
		x[2*i] = noFMA32Mul(haarScale, a) + noFMA32Mul(haarScale, b)
		x[2*i+1] = noFMA32Mul(haarScale, a) - noFMA32Mul(haarScale, b)
	}
}

func haar1Stride2NEON(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	_ = x[4*n0-1]
	p := unsafe.Pointer(unsafe.SliceData(x))
	scale := archsimd.BroadcastFloat32x4(haarScale)
	i := 0
	for ; i+2 <= n0; i += 2 {
		off := unsafe.Add(p, i*16)
		a := loadF32x4(off).ToBits().ReshapeToUint64s()
		b := loadF32x4(unsafe.Add(off, 16)).ToBits().ReshapeToUint64s()
		lo := a.InterleaveLo(b).ReshapeToUint32s().BitsToFloat32()
		hi := a.InterleaveHi(b).ReshapeToUint32s().BitsToFloat32()
		sum := lo.Mul(scale).Add(hi.Mul(scale)).ToBits()
		diff := lo.Mul(scale).Sub(hi.Mul(scale)).ToBits()
		sum64 := sum.ReshapeToUint64s()
		diff64 := diff.ReshapeToUint64s()
		storeF32x4(off, sum64.InterleaveLo(diff64).ReshapeToUint32s().BitsToFloat32())
		storeF32x4(unsafe.Add(off, 16), sum64.InterleaveHi(diff64).ReshapeToUint32s().BitsToFloat32())
	}
	for ; i < n0; i++ {
		off := 4 * i
		a, b, c, d := x[off], x[off+1], x[off+2], x[off+3]
		x[off] = noFMA32Mul(haarScale, a) + noFMA32Mul(haarScale, c)
		x[off+1] = noFMA32Mul(haarScale, b) + noFMA32Mul(haarScale, d)
		x[off+2] = noFMA32Mul(haarScale, a) - noFMA32Mul(haarScale, c)
		x[off+3] = noFMA32Mul(haarScale, b) - noFMA32Mul(haarScale, d)
	}
}

func haar1Stride4NEON(x []float32, n0 int) {
	if n0 <= 0 {
		return
	}
	_ = x[8*n0-1]
	p := unsafe.Pointer(unsafe.SliceData(x))
	scale := archsimd.BroadcastFloat32x4(haarScale)
	for i := 0; i < n0; i++ {
		off := unsafe.Add(p, i*32)
		lo := loadF32x4(off)
		hi := loadF32x4(unsafe.Add(off, 16))
		scaledLo := lo.Mul(scale)
		scaledHi := hi.Mul(scale)
		storeF32x4(off, scaledLo.Add(scaledHi))
		storeF32x4(unsafe.Add(off, 16), scaledLo.Sub(scaledHi))
	}
}
