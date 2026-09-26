//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// innerProdFloat32SSEOrder reproduces libopus x86/pitch_sse.c
// celt_inner_prod_sse: one 4-lane MULPS/ADDPS accumulator, the
// (a0+a2)+(a1+a3) reduction, and a separate multiply/add scalar tail. The
// archsimd lanes run exactly that operation sequence, so the result is
// bit-identical to innerProdFloat32SSEOrderScalar.
func innerProdFloat32SSEOrder(x, y []float32, length int) float32 {
	if length <= 0 {
		return 0
	}
	if !archsimd.X86.AVX() {
		return innerProdFloat32SSEOrderScalar(x, y, length)
	}
	x = x[:length]
	y = y[:length]
	xp := unsafe.Pointer(unsafe.SliceData(x))
	yp := unsafe.Pointer(unsafe.SliceData(y))
	var acc archsimd.Float32x4
	i := 0
	for ; i+4 <= length; i += 4 {
		off := uintptr(i) * 4
		acc = acc.Add(loadF32x4(unsafe.Add(xp, off)).Mul(loadF32x4(unsafe.Add(yp, off))))
	}
	sum := add32(add32(acc.GetElem(0), acc.GetElem(2)), add32(acc.GetElem(1), acc.GetElem(3)))
	for ; i < length; i++ {
		sum = add32(sum, mul32(x[i], y[i]))
	}
	if sum != sum {
		return opusmath.PitchXcorrSSENaNReplay(x, y, length)
	}
	return sum
}

// prefilterDualInnerProdF32SSEOrder reproduces libopus x86/pitch_sse.c
// dual_inner_prod_sse: two 4-lane MULPS/ADDPS accumulators sharing each x
// load, the (a0+a2)+(a1+a3) reductions, and a separate multiply/add scalar
// tail. The archsimd lanes run exactly that operation sequence, so the result
// is bit-identical to prefilterDualInnerProdF32SSEOrderScalar.
func prefilterDualInnerProdF32SSEOrder(x, y1, y2 []float32, length int) (float32, float32) {
	if length <= 0 {
		return 0, 0
	}
	if !archsimd.X86.AVX() {
		return prefilterDualInnerProdF32SSEOrderScalar(x, y1, y2, length)
	}
	x = x[:length]
	y1 = y1[:length]
	y2 = y2[:length]
	xp := unsafe.Pointer(unsafe.SliceData(x))
	y1p := unsafe.Pointer(unsafe.SliceData(y1))
	y2p := unsafe.Pointer(unsafe.SliceData(y2))
	var acc1, acc2 archsimd.Float32x4
	i := 0
	for ; i+4 <= length; i += 4 {
		off := uintptr(i) * 4
		vx := loadF32x4(unsafe.Add(xp, off))
		acc1 = acc1.Add(vx.Mul(loadF32x4(unsafe.Add(y1p, off))))
		acc2 = acc2.Add(vx.Mul(loadF32x4(unsafe.Add(y2p, off))))
	}
	sum1 := add32(add32(acc1.GetElem(0), acc1.GetElem(2)), add32(acc1.GetElem(1), acc1.GetElem(3)))
	sum2 := add32(add32(acc2.GetElem(0), acc2.GetElem(2)), add32(acc2.GetElem(1), acc2.GetElem(3)))
	for ; i < length; i++ {
		xi := x[i]
		sum1 = add32(sum1, mul32(xi, y1[i]))
		sum2 = add32(sum2, mul32(xi, y2[i]))
	}
	return sum1, sum2
}
