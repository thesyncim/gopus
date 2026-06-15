//go:build arm64 && goexperiment.simd && !purego

package celt

import "simd/archsimd"

// stereoMergeRescaleNEON applies the final mid/side rescale of stereoMerge over
// len(x) lanes in place with archsimd (4-wide NEON Float32x4):
//
//	l    = mid * x[i]
//	x[i] = lgain * (l - y[i])
//	y[i] = rgain * (l + y[i])
//
// Mul, Sub and Add are distinct archsimd ops — no fused multiply-add forms across
// separate results — so each lane keeps the two-rounding shape of the scalar
// noFMA32 reference and the hand asm, and the result is bit-exact.
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	if n <= 0 {
		return
	}
	x = x[:n]
	y = y[:n]
	midv := archsimd.BroadcastFloat32x4(mid)
	lv := archsimd.BroadcastFloat32x4(lgain)
	rv := archsimd.BroadcastFloat32x4(rgain)
	i := 0
	for ; i+4 <= n; i += 4 {
		xv := archsimd.LoadFloat32x4(x[i:])
		yv := archsimd.LoadFloat32x4(y[i:])
		l := midv.Mul(xv)
		lv.Mul(l.Sub(yv)).Store(x[i:])
		rv.Mul(l.Add(yv)).Store(y[i:])
	}
	for ; i < n; i++ {
		l := noFMA32Mul(mid, x[i])
		r := y[i]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l, r))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l, r))
	}
}
