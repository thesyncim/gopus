//go:build arm64 && goexperiment.simd && !nosimd

package gopus

import (
	"simd/archsimd"
	"unsafe"
)

func convertFloat32ToInt16Unit(dst []int16, src []float32, n int) bool {
	if n <= 0 {
		return true
	}
	_ = dst[n-1]
	_ = src[n-1]
	blocks := n &^ 15
	if blocks != 0 && !convertFloat32ToInt16UnitBlocks(dst, src, blocks) {
		return false
	}
	for i := blocks; i < n; i++ {
		v := src[i]
		if !(v >= -1 && v <= 1) {
			return false
		}
		dst[i] = float32ToInt16(v)
	}
	return true
}

func convertFloat32ToInt16NoSoftClipUnit(dst []int16, src []float32, n int) {
	if n <= 0 {
		return
	}
	_ = dst[n-1]
	_ = src[n-1]
	blocks := n &^ 15
	if blocks != 0 {
		convertFloat32ToInt16SaturatingBlocks(dst, src, blocks)
	}
	for i := blocks; i < n; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}

func convertFloat32ToInt16UnitBlocks(dst []int16, src []float32, n int) bool {
	if n == 0 {
		return true
	}
	_ = dst[n-1]
	_ = src[n-1]
	sp := unsafe.Pointer(unsafe.SliceData(src))
	dp := unsafe.Pointer(unsafe.SliceData(dst))
	one := archsimd.BroadcastFloat32x4(1)
	scale := archsimd.BroadcastFloat32x4(32768)
	max := archsimd.BroadcastInt32x4(32767)
	for i := 0; i < n; i += 16 {
		v0 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(sp, i*4)))
		if v0.Abs().LessEqual(one).ToInt32x4().ReduceMax() != -1 {
			return false
		}
		q0 := v0.Mul(scale).Round().ConvertToInt32().Min(max)

		v1 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(sp, i*4+16)))
		if v1.Abs().LessEqual(one).ToInt32x4().ReduceMax() != -1 {
			return false
		}
		q1 := v1.Mul(scale).Round().ConvertToInt32().Min(max)
		storeInt16x8((*[8]int16)(unsafe.Add(dp, i*2)), q0, q1)

		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(sp, i*4+32)))
		if v2.Abs().LessEqual(one).ToInt32x4().ReduceMax() != -1 {
			return false
		}
		q2 := v2.Mul(scale).Round().ConvertToInt32().Min(max)

		v3 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(sp, i*4+48)))
		if v3.Abs().LessEqual(one).ToInt32x4().ReduceMax() != -1 {
			return false
		}
		q3 := v3.Mul(scale).Round().ConvertToInt32().Min(max)
		storeInt16x8((*[8]int16)(unsafe.Add(dp, i*2+16)), q2, q3)
	}
	return true
}

func convertFloat32ToInt16SaturatingBlocks(dst []int16, src []float32, n int) {
	if n == 0 {
		return
	}
	_ = dst[n-1]
	_ = src[n-1]
	sp := unsafe.Pointer(unsafe.SliceData(src))
	dp := unsafe.Pointer(unsafe.SliceData(dst))
	scale := archsimd.BroadcastFloat32x4(32768)
	for i := 0; i < n; i += 16 {
		v0 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(sp, i*4)))
		q0 := v0.Mul(scale).Round().ConvertToInt32()

		v1 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(sp, i*4+16)))
		q1 := v1.Mul(scale).Round().ConvertToInt32()
		storeInt16x8((*[8]int16)(unsafe.Add(dp, i*2)), q0, q1)

		v2 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(sp, i*4+32)))
		q2 := v2.Mul(scale).Round().ConvertToInt32()

		v3 := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(sp, i*4+48)))
		q3 := v3.Mul(scale).Round().ConvertToInt32()
		storeInt16x8((*[8]int16)(unsafe.Add(dp, i*2+16)), q2, q3)
	}
}

func storeInt16x8(dst *[8]int16, lo, hi archsimd.Int32x4) {
	lo16 := lo.SaturateToInt16().ToBits().ReshapeToUint64s()
	hi16 := hi.SaturateToInt16().ToBits().ReshapeToUint64s()
	lo16.ConcatEven(hi16).ReshapeToUint16s().BitsToInt16().StoreArray(dst)
}
