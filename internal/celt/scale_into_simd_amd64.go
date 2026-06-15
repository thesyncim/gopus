//go:build amd64 && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// scaleFloat32IntoNEON computes dst[i] = src[i]*gain over min(len(dst),len(src))
// elements. On an AVX CPU it scales eight lanes per VMULPS through the 256-bit
// Float32x8; the 128-bit Float32x4 path clears the tail and carries CPUs without
// AVX. Loads/stores go through raw pointers (loadF32x8/loadF32x4) so there is no
// per-access slice bounds check. Each lane is a bare per-lane multiply — the same
// single-rounding product as the scalar reference — so the result is bit-exact.
func scaleFloat32IntoNEON(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	sp := unsafe.Pointer(unsafe.SliceData(src))
	dp := unsafe.Pointer(unsafe.SliceData(dst))
	i := 0
	if archsimd.X86.AVX() {
		g8 := archsimd.BroadcastFloat32x8(gain)
		for ; i+8 <= n; i += 8 {
			storeF32x8(dp, loadF32x8(sp).Mul(g8))
			sp = unsafe.Add(sp, 32)
			dp = unsafe.Add(dp, 32)
		}
	}
	g4 := archsimd.BroadcastFloat32x4(gain)
	for ; i+4 <= n; i += 4 {
		storeF32x4(dp, loadF32x4(sp).Mul(g4))
		sp = unsafe.Add(sp, 16)
		dp = unsafe.Add(dp, 16)
	}
	for ; i < n; i++ {
		*(*float32)(dp) = noFMA32Mul(*(*float32)(sp), gain)
		sp = unsafe.Add(sp, 4)
		dp = unsafe.Add(dp, 4)
	}
}
