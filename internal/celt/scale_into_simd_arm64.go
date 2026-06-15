//go:build arm64 && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// scaleFloat32IntoNEON computes dst[i] = src[i]*gain over min(len(dst),len(src))
// elements as 4-wide NEON FMULs (Float32x4.Mul) — each lane the same
// single-rounding product as the scalar reference and the hand asm, so the
// result is bit-exact.
//
// It loads through *[4]float32 views over advancing unsafe pointers instead of
// archsimd.LoadFloat32x4(src[i:]). The slice form emits a length bounds check and
// a panic path on every load and store, and that check machinery — not the SIMD —
// dominates this load/store-bound kernel (3-5x slower, slower even than scalar
// asm). With the checks gone and an 8-wide unroll the archsimd loop beats the
// hand NEON asm. Safety: the pointers only advance while i+k <= n, so every read
// and write stays within the first n elements; an empty slice skips all loops, so
// SliceData is never dereferenced.
func scaleFloat32IntoNEON(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	g := archsimd.BroadcastFloat32x4(gain)
	sp := unsafe.Pointer(unsafe.SliceData(src))
	dp := unsafe.Pointer(unsafe.SliceData(dst))
	i := 0
	for ; i+8 <= n; i += 8 {
		storeF32x4(dp, loadF32x4(sp).Mul(g))
		storeF32x4(unsafe.Add(dp, 16), loadF32x4(unsafe.Add(sp, 16)).Mul(g))
		sp = unsafe.Add(sp, 32)
		dp = unsafe.Add(dp, 32)
	}
	for ; i+4 <= n; i += 4 {
		storeF32x4(dp, loadF32x4(sp).Mul(g))
		sp = unsafe.Add(sp, 16)
		dp = unsafe.Add(dp, 16)
	}
	for ; i < n; i++ {
		*(*float32)(dp) = noFMA32Mul(*(*float32)(sp), gain)
		sp = unsafe.Add(sp, 4)
		dp = unsafe.Add(dp, 4)
	}
}
