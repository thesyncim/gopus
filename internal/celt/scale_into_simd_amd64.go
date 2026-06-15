//go:build amd64 && goexperiment.simd && !purego

package celt

import "simd/archsimd"

// scaleFloat32IntoNEON computes dst[i] = src[i]*gain over min(len(dst),len(src))
// elements with archsimd. On an AVX CPU it scales eight lanes per VMULPS through
// the 256-bit Float32x8; the 128-bit Float32x4 path then clears the 4..7 tail and
// also carries CPUs without AVX. Each lane is a bare per-lane multiply — the same
// single-rounding product as the scalar reference — so the result is bit-exact
// regardless of which width handled a given element.
func scaleFloat32IntoNEON(dst, src []float32, gain float32) {
	n := min(len(dst), len(src))
	i := 0
	if archsimd.X86.AVX() {
		g8 := archsimd.BroadcastFloat32x8(gain)
		for ; i+8 <= n; i += 8 {
			archsimd.LoadFloat32x8(src[i:]).Mul(g8).Store(dst[i:])
		}
	}
	g4 := archsimd.BroadcastFloat32x4(gain)
	for ; i+4 <= n; i += 4 {
		archsimd.LoadFloat32x4(src[i:]).Mul(g4).Store(dst[i:])
	}
	for ; i < n; i++ {
		dst[i] = noFMA32Mul(src[i], gain)
	}
}
