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
		for j := 0; j < 16; j += 4 {
			off := i + j
			v := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(sp, off*4)))
			mask := v.Abs().LessEqual(one).ToInt32x4()
			if mask.GetElem(0) != -1 || mask.GetElem(1) != -1 || mask.GetElem(2) != -1 || mask.GetElem(3) != -1 {
				return false
			}
			q := v.Mul(scale).Round().ConvertToInt32().Min(max).SaturateToInt16()
			q.StorePart((*[4]int16)(unsafe.Add(dp, off*2))[:])
		}
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
		for j := 0; j < 16; j += 4 {
			off := i + j
			v := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(sp, off*4)))
			q := v.Mul(scale).Round().ConvertToInt32().SaturateToInt16()
			q.StorePart((*[4]int16)(unsafe.Add(dp, off*2))[:])
		}
	}
}
