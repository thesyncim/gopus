//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"
)

func celtInnerProdSSEStyleImpl(x, y []celtNorm) float32 {
	return celtInnerProdSSEStyleDispatch(x, y, archsimd.X86.AVX())
}

func celtInnerProdSSEStyleDispatch(x, y []celtNorm, avx bool) float32 {
	if !avx {
		return celtInnerProdSSEStyleGo(x, y)
	}
	return celtInnerProdSSEStyleSIMD(x, y)
}

func celtInnerProdSSEStyleSIMD(x, y []celtNorm) float32 {
	n := min(len(x), len(y))
	var acc archsimd.Float32x4
	i := 0
	for ; i+4 <= n; i += 4 {
		vx := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&x[i])))
		vy := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Pointer(&y[i])))
		acc = acc.Add(vx.Mul(vy))
	}
	sum0 := round32(acc.GetElem(0) + acc.GetElem(2))
	sum1 := round32(acc.GetElem(1) + acc.GetElem(3))
	sum := round32(sum0 + sum1)
	for ; i < n; i++ {
		sum = celtFloatMulAdd(float32(x[i]), float32(y[i]), sum)
	}
	return sum
}
