//go:build arm64 && goexperiment.simd && !purego

package celt

import "simd/archsimd"

// scaleFloat32IntoNEON computes dst[i] = src[i]*gain over min(len(dst),len(src))
// elements with archsimd: a broadcast gain times a 4-wide NEON load (Float32x4),
// stored back, with a scalar tail. NEON registers are 128-bit, so four lanes is
// the widest single multiply; eight per iteration is two such vectors and, on
// this load/store-bound kernel, benchmarks no faster than four, so the loop
// stays 4-wide. Each lane is a bare FMUL — the same single-rounding product as
// the scalar reference and the hand-written asm — so the result is bit-exact.
func scaleFloat32IntoNEON(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	g := archsimd.BroadcastFloat32x4(gain)
	i := 0
	for ; i+4 <= n; i += 4 {
		archsimd.LoadFloat32x4(src[i:]).Mul(g).Store(dst[i:])
	}
	for ; i < n; i++ {
		dst[i] = noFMA32Mul(src[i], gain)
	}
}
