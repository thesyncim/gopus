//go:build amd64 && goexperiment.simd && !purego

package celt

import "simd/archsimd"

// stereoMergeRescaleNEON applies the final mid/side rescale of stereoMerge over
// len(x) lanes in place with archsimd:
//
//	l    = mid * x[i]
//	x[i] = lgain * (l - y[i])
//	y[i] = rgain * (l + y[i])
//
// On an AVX CPU it processes eight lanes per step through the 256-bit Float32x8;
// the 128-bit Float32x4 path clears the remainder and carries CPUs without AVX.
// Mul, Sub and Add are distinct archsimd ops — no fused multiply-add across
// separate results — so each lane keeps the two-rounding shape of the scalar
// noFMA32 reference, bit-exact regardless of which width handled it.
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	if n <= 0 {
		return
	}
	x = x[:n]
	y = y[:n]
	i := 0
	if archsimd.X86.AVX() {
		midv := archsimd.BroadcastFloat32x8(mid)
		lv := archsimd.BroadcastFloat32x8(lgain)
		rv := archsimd.BroadcastFloat32x8(rgain)
		for ; i+8 <= n; i += 8 {
			xv := archsimd.LoadFloat32x8(x[i:])
			yv := archsimd.LoadFloat32x8(y[i:])
			l := midv.Mul(xv)
			lv.Mul(l.Sub(yv)).Store(x[i:])
			rv.Mul(l.Add(yv)).Store(y[i:])
		}
	}
	midv := archsimd.BroadcastFloat32x4(mid)
	lv := archsimd.BroadcastFloat32x4(lgain)
	rv := archsimd.BroadcastFloat32x4(rgain)
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
