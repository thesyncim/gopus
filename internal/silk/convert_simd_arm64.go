//go:build arm64 && goexperiment.simd && !nosimd

package silk

import (
	"unsafe"

	"simd/archsimd"
)

func floatToInt16Scaled(out []int16, in []float32, scale float32, n int) {
	n8 := n &^ 7
	if n8 != 0 {
		floatToInt16ScaledCore(out, in, scale, n8)
	}
	for i := n8; i < n; i++ {
		out[i] = floatToInt16Round(in[i] * scale)
	}
}

func floatToInt16ScaledCore(out []int16, in []float32, scale float32, n int) {
	if n == 0 {
		return
	}
	_ = out[n-1]
	_ = in[n-1]
	op := unsafe.Pointer(unsafe.SliceData(out))
	ip := unsafe.Pointer(unsafe.SliceData(in))
	// Pitch detection uses scale 1, so the SIMD conversion can omit the multiply.
	if scale == 1 {
		remaining := n
		for remaining > 8 {
			x := archsimd.LoadFloat32x4Array((*[4]float32)(ip))
			y := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(ip, 16)))
			a := x.Round().ConvertToInt32().SaturateToInt16()
			b := y.Round().ConvertToInt32().SaturateToInt16()
			packed := a.ToBits().ReshapeToUint64s().InterleaveLo(b.ToBits().ReshapeToUint64s()).ReshapeToUint16s().BitsToInt16()
			packed.StoreArray((*[8]int16)(op))
			ip = unsafe.Add(ip, 32)
			op = unsafe.Add(op, 16)
			remaining -= 8
		}
		x := archsimd.LoadFloat32x4Array((*[4]float32)(ip))
		y := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(ip, 16)))
		a := x.Round().ConvertToInt32().SaturateToInt16()
		b := y.Round().ConvertToInt32().SaturateToInt16()
		packed := a.ToBits().ReshapeToUint64s().InterleaveLo(b.ToBits().ReshapeToUint64s()).ReshapeToUint16s().BitsToInt16()
		packed.StoreArray((*[8]int16)(op))
		return
	}
	gain := archsimd.BroadcastFloat32x4(scale)
	for i := 0; i < n; i += 8 {
		a := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(ip, i*4))).Mul(gain).Round().ConvertToInt32().SaturateToInt16()
		b := archsimd.LoadFloat32x4Array((*[4]float32)(unsafe.Add(ip, (i+4)*4))).Mul(gain).Round().ConvertToInt32().SaturateToInt16()
		packed := a.ToBits().ReshapeToUint64s().InterleaveLo(b.ToBits().ReshapeToUint64s()).ReshapeToUint16s().BitsToInt16()
		packed.StoreArray((*[8]int16)(unsafe.Add(op, i*2)))
	}
}

func writeInt16AsFloat32Core(dst []float32, src []int16, n int) {
	if n == 0 {
		return
	}
	_ = dst[n-1]
	_ = src[n-1]
	const inv32768 = float32(1.0 / 32768.0)
	gain := archsimd.BroadcastFloat32x4(inv32768)
	dp := unsafe.Pointer(unsafe.SliceData(dst))
	sp := unsafe.Pointer(unsafe.SliceData(src))
	i := 0
	for ; i+8 <= n; i += 8 {
		v := archsimd.LoadInt16x8Array((*[8]int16)(unsafe.Add(sp, i*2)))
		lo := v.ExtendLo4ToInt32().ConvertToFloat32().Mul(gain)
		hi := v.HiToLo().ExtendLo4ToInt32().ConvertToFloat32().Mul(gain)
		lo.StoreArray((*[4]float32)(unsafe.Add(dp, i*4)))
		hi.StoreArray((*[4]float32)(unsafe.Add(dp, (i+4)*4)))
	}
	for ; i < n; i++ {
		dst[i] = float32(src[i]) * inv32768
	}
}
