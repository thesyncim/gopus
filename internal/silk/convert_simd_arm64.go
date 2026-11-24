//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

func floatToInt16Scaled(out []int16, in []float32, scale float32, n int) {
	n8 := n &^ 7
	if n8 != 0 {
		floatToInt16ScaledCore(out[:n8], in[:n8], scale)
	}
	for i := n8; i < n; i++ {
		out[i] = floatToInt16Round(noFMA32(in[i], scale))
	}
}

func floatToInt16ScaledCore(out []int16, in []float32, scale float32) {
	n := len(out)
	in = in[:n]
	// Pitch detection uses scale 1, so the SIMD conversion can omit the multiply.
	if scale == 1 {
		for i := 0; i+8 <= n; i += 8 {
			x := archsimd.LoadFloat32x4Array((*[4]float32)(in[i:]))
			y := archsimd.LoadFloat32x4Array((*[4]float32)(in[i+4:]))
			a := x.Round().ConvertToInt32().SaturateToInt16()
			b := y.Round().ConvertToInt32().SaturateToInt16()
			packed := a.ToBits().ReshapeToUint64s().InterleaveLo(b.ToBits().ReshapeToUint64s()).ReshapeToUint16s().BitsToInt16()
			packed.StoreArray((*[8]int16)(out[i:]))
		}
		return
	}
	gain := archsimd.BroadcastFloat32x4(scale)
	for i := 0; i+8 <= n; i += 8 {
		a := archsimd.LoadFloat32x4Array((*[4]float32)(in[i:])).Mul(gain).Round().ConvertToInt32().SaturateToInt16()
		b := archsimd.LoadFloat32x4Array((*[4]float32)(in[i+4:])).Mul(gain).Round().ConvertToInt32().SaturateToInt16()
		packed := a.ToBits().ReshapeToUint64s().InterleaveLo(b.ToBits().ReshapeToUint64s()).ReshapeToUint16s().BitsToInt16()
		packed.StoreArray((*[8]int16)(out[i:]))
	}
}

func writeInt16AsFloat32Core(dst []float32, src []int16, n int) {
	if n <= 0 {
		return
	}
	dst = dst[:n]
	src = src[:n]
	const inv32768 = float32(1.0 / 32768.0)
	gain := archsimd.BroadcastFloat32x4(inv32768)
	i := 0
	for ; i+8 <= n; i += 8 {
		v := archsimd.LoadInt16x8Array((*[8]int16)(src[i:]))
		lo := v.ExtendLo4ToInt32().ConvertToFloat32().Mul(gain)
		hi := v.HiToLo().ExtendLo4ToInt32().ConvertToFloat32().Mul(gain)
		lo.StoreArray((*[4]float32)(dst[i:]))
		hi.StoreArray((*[4]float32)(dst[i+4:]))
	}
	for ; i < n; i++ {
		dst[i] = float32(src[i]) * inv32768
	}
}

func interleaveInt16AsFloat32Core(dst []float32, left, right []int16) {
	const inv32768 = float32(1.0 / 32768.0)
	n := len(left)
	if n <= 0 {
		return
	}
	left = left[:n:n]
	right = right[:n:n]
	dst = dst[: 2*n : 2*n]
	gain := archsimd.BroadcastFloat32x4(inv32768)
	i := 0
	for ; i+8 <= n; i += 8 {
		l := archsimd.LoadInt16x8Array((*[8]int16)(left[i : i+8]))
		r := archsimd.LoadInt16x8Array((*[8]int16)(right[i : i+8]))
		lLo := l.ExtendLo4ToInt32().ConvertToFloat32().Mul(gain).ToBits()
		rLo := r.ExtendLo4ToInt32().ConvertToFloat32().Mul(gain).ToBits()
		k := 2 * i
		lLo.InterleaveLo(rLo).BitsToFloat32().StoreArray((*[4]float32)(dst[k : k+4]))
		lLo.InterleaveHi(rLo).BitsToFloat32().StoreArray((*[4]float32)(dst[k+4 : k+8]))
		lHi := l.HiToLo().ExtendLo4ToInt32().ConvertToFloat32().Mul(gain).ToBits()
		rHi := r.HiToLo().ExtendLo4ToInt32().ConvertToFloat32().Mul(gain).ToBits()
		lHi.InterleaveLo(rHi).BitsToFloat32().StoreArray((*[4]float32)(dst[k+8 : k+12]))
		lHi.InterleaveHi(rHi).BitsToFloat32().StoreArray((*[4]float32)(dst[k+12 : k+16]))
	}
	for ; i < n; i++ {
		dst[2*i] = float32(left[i]) * inv32768
		dst[2*i+1] = float32(right[i]) * inv32768
	}
}

func duplicateMonoFloat32ToStereoCore(dst, src []float32, n int) {
	if n <= 0 {
		return
	}
	dst = dst[: n*2 : n*2]
	src = src[:n:n]
	i := 0
	for ; i+4 <= n; i += 4 {
		b := archsimd.LoadFloat32x4Array((*[4]float32)(src[i : i+4])).ToBits()
		k := 2 * i
		b.InterleaveLo(b).BitsToFloat32().StoreArray((*[4]float32)(dst[k : k+4]))
		b.InterleaveHi(b).BitsToFloat32().StoreArray((*[4]float32)(dst[k+4 : k+8]))
	}
	for ; i < n; i++ {
		v := src[i]
		dst[2*i] = v
		dst[2*i+1] = v
	}
}
