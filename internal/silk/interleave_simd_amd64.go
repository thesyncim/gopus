//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

var silkInterleaveUsesAVX = archsimd.X86.AVX()

func interleaveInt16AsFloat32Core(dst []float32, left, right []int16) {
	const inv32768 = float32(1.0 / 32768.0)
	n := len(left)
	if n <= 0 {
		return
	}
	left = left[:n:n]
	right = right[:n:n]
	dst = dst[: 2*n : 2*n]
	i := 0
	if int16ToFloat32UsesAVX2 && n >= 8 {
		i = interleaveInt16AsFloat32CoreAVX2(dst, left, right)
	}
	for ; i < n; i++ {
		dst[2*i] = float32(left[i]) * inv32768
		dst[2*i+1] = float32(right[i]) * inv32768
	}
}

//go:noinline
func interleaveInt16AsFloat32CoreAVX2(dst []float32, left, right []int16) int {
	const inv32768 = float32(1.0 / 32768.0)
	n := len(left)
	scale := archsimd.BroadcastFloat32x4(inv32768)
	var zero archsimd.Int16x8
	i := 0
	for ; i+8 <= n; i += 8 {
		l := archsimd.LoadInt16x8Array((*[8]int16)(left[i : i+8]))
		r := archsimd.LoadInt16x8Array((*[8]int16)(right[i : i+8]))
		lLo := l.ExtendLo4ToInt32().ConvertToFloat32().Mul(scale).ToBits()
		rLo := r.ExtendLo4ToInt32().ConvertToFloat32().Mul(scale).ToBits()
		k := 2 * i
		lLo.InterleaveLo(rLo).BitsToFloat32().StoreArray((*[4]float32)(dst[k : k+4]))
		lLo.InterleaveHi(rLo).BitsToFloat32().StoreArray((*[4]float32)(dst[k+4 : k+8]))

		lHi := zero.InterleaveHi(l).AsInt32x4().ShiftAllRight(16).ConvertToFloat32().Mul(scale).ToBits()
		rHi := zero.InterleaveHi(r).AsInt32x4().ShiftAllRight(16).ConvertToFloat32().Mul(scale).ToBits()
		lHi.InterleaveLo(rHi).BitsToFloat32().StoreArray((*[4]float32)(dst[k+8 : k+12]))
		lHi.InterleaveHi(rHi).BitsToFloat32().StoreArray((*[4]float32)(dst[k+12 : k+16]))
	}
	return i
}

func duplicateMonoFloat32ToStereoCore(dst, src []float32, n int) {
	if n <= 0 {
		return
	}
	dst = dst[: n*2 : n*2]
	src = src[:n:n]
	i := 0
	if silkInterleaveUsesAVX && n >= 4 {
		i = duplicateMonoFloat32ToStereoCoreAVX(dst, src, n)
	}
	for ; i < n; i++ {
		v := src[i]
		dst[2*i] = v
		dst[2*i+1] = v
	}
}

//go:noinline
func duplicateMonoFloat32ToStereoCoreAVX(dst, src []float32, n int) int {
	i := 0
	for ; i+4 <= n; i += 4 {
		b := archsimd.LoadFloat32x4Array((*[4]float32)(src[i : i+4])).ToBits()
		k := 2 * i
		b.InterleaveLo(b).BitsToFloat32().StoreArray((*[4]float32)(dst[k : k+4]))
		b.InterleaveHi(b).BitsToFloat32().StoreArray((*[4]float32)(dst[k+4 : k+8]))
	}
	return i
}
