//go:build (amd64 || arm64) && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// scaleFloat32Into computes dst[i] = src[i]*gain over min(len(dst),len(src))
// elements as 4-wide Float32x4.Mul products: each lane is the same
// single-rounding product as the scalar loop, so the result is bit-exact.
func scaleFloat32Into(dst, src []float32, gain float32) {
	if !hasCELTFloat32x4SIMD() {
		scaleFloat32IntoScalar(dst, src, gain)
		return
	}
	scaleFloat32IntoArchSIMD(dst, src, gain)
}

//go:noinline
func scaleFloat32IntoArchSIMD(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	if n <= 0 {
		return
	}
	dst = dst[:n]
	src = src[:n]
	g := broadcastF32x4Arch(gain)
	i := 0
	for ; i+16 <= n; i += 16 {
		archsimd.LoadFloat32x4Array((*[4]float32)(src[i:])).Mul(g).StoreArray((*[4]float32)(dst[i:]))
		archsimd.LoadFloat32x4Array((*[4]float32)(src[i+4:])).Mul(g).StoreArray((*[4]float32)(dst[i+4:]))
		archsimd.LoadFloat32x4Array((*[4]float32)(src[i+8:])).Mul(g).StoreArray((*[4]float32)(dst[i+8:]))
		archsimd.LoadFloat32x4Array((*[4]float32)(src[i+12:])).Mul(g).StoreArray((*[4]float32)(dst[i+12:]))
	}
	for ; i+4 <= n; i += 4 {
		archsimd.LoadFloat32x4Array((*[4]float32)(src[i:])).Mul(g).StoreArray((*[4]float32)(dst[i:]))
	}
	for ; i < n; i++ {
		dst[i] = noFMA32Mul(src[i], gain)
	}
}

func scaleFloat32IntoScalar(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = noFMA32Mul(src[i], gain)
	}
}
