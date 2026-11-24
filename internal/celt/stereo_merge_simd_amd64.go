//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var stereoMergeUsesAVX = archsimd.X86.AVX()
var stereoMergeUsesAVX2 = archsimd.X86.AVX2() && (!stereoMergeUsesFMA || archsimd.X86.FMA())

// stereoMergeRescaleNEON is the final loop of libopus celt/bands.c
// stereo_merge, l = mid*x[j], x[j] = lgain*(l-y[j]), y[j] = rgain*(l+y[j]).
// Eight lanes per step on AVX2 (or four on AVX1) compute the same per-lane
// products and sums.
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	y = y[:n]
	i := 0
	if stereoMergeUsesFMA {
		if stereoMergeUsesAVX2 && n >= 8 {
			i = stereoMergeRescaleAVX2FMA(x, y, mid, lgain, rgain)
		} else if stereoMergeUsesAVX && n >= 4 {
			i = stereoMergeRescaleAVXFMA(x, y, mid, lgain, rgain)
		}
		for ; i < n; i++ {
			xv, yv := x[i], y[i]
			left := fma32(mid, xv, -yv)
			right := fma32(mid, xv, yv)
			x[i] = noFMA32Mul(lgain, left)
			y[i] = noFMA32Mul(rgain, right)
		}
		return
	}
	if stereoMergeUsesAVX2 && n >= 8 {
		i = stereoMergeRescaleAVX2(x, y, mid, lgain, rgain)
	} else if stereoMergeUsesAVX && n >= 4 {
		i = stereoMergeRescaleAVX(x, y, mid, lgain, rgain)
	}
	for ; i < n; i++ {
		l := noFMA32Mul(mid, x[i])
		r := y[i]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l, r))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l, r))
	}
}

//go:noinline
func stereoMergeRescaleAVX2FMA(x, y []float32, mid, lgain, rgain float32) int {
	n := len(x)
	midv := archsimd.BroadcastFloat32x8(mid)
	lg := archsimd.BroadcastFloat32x8(lgain)
	rg := archsimd.BroadcastFloat32x8(rgain)
	i := 0
	for ; i+8 <= n; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(x[i:]))
		yv := archsimd.LoadFloat32x8Array((*[8]float32)(y[i:]))
		left := midv.MulAdd(xv, yv.Neg())
		right := midv.MulAdd(xv, yv)
		lg.Mul(left).StoreArray((*[8]float32)(x[i:]))
		rg.Mul(right).StoreArray((*[8]float32)(y[i:]))
	}
	archsimd.ClearAVXUpperBits()
	if i+4 <= n {
		midv4 := broadcastF32x4Arch(mid)
		lg4 := broadcastF32x4Arch(lgain)
		rg4 := broadcastF32x4Arch(rgain)
		xv := archsimd.LoadFloat32x4Array((*[4]float32)(x[i:]))
		yv := archsimd.LoadFloat32x4Array((*[4]float32)(y[i:]))
		left := midv4.MulAdd(xv, yv.Neg())
		right := midv4.MulAdd(xv, yv)
		lg4.Mul(left).StoreArray((*[4]float32)(x[i:]))
		rg4.Mul(right).StoreArray((*[4]float32)(y[i:]))
		i += 4
	}
	return i
}

//go:noinline
func stereoMergeRescaleAVX2(x, y []float32, mid, lgain, rgain float32) int {
	n := len(x)
	midv := archsimd.BroadcastFloat32x8(mid)
	lg := archsimd.BroadcastFloat32x8(lgain)
	rg := archsimd.BroadcastFloat32x8(rgain)
	i := 0
	for ; i+8 <= n; i += 8 {
		l := midv.Mul(archsimd.LoadFloat32x8Array((*[8]float32)(x[i:])))
		r := archsimd.LoadFloat32x8Array((*[8]float32)(y[i:]))
		lg.Mul(l.Sub(r)).StoreArray((*[8]float32)(x[i:]))
		rg.Mul(l.Add(r)).StoreArray((*[8]float32)(y[i:]))
	}
	archsimd.ClearAVXUpperBits()
	if i+4 <= n {
		midv4 := broadcastF32x4Arch(mid)
		lg4 := broadcastF32x4Arch(lgain)
		rg4 := broadcastF32x4Arch(rgain)
		l := midv4.Mul(archsimd.LoadFloat32x4Array((*[4]float32)(x[i:])))
		r := archsimd.LoadFloat32x4Array((*[4]float32)(y[i:]))
		lg4.Mul(l.Sub(r)).StoreArray((*[4]float32)(x[i:]))
		rg4.Mul(l.Add(r)).StoreArray((*[4]float32)(y[i:]))
		i += 4
	}
	return i
}

//go:noinline
func stereoMergeRescaleAVXFMA(x, y []float32, mid, lgain, rgain float32) int {
	n := len(x) &^ 3
	x = x[:n]
	y = y[:n]
	midv := broadcastF32x4Arch(mid)
	lg := broadcastF32x4Arch(lgain)
	rg := broadcastF32x4Arch(rgain)
	for i := 0; i < n; i += 4 {
		xv := archsimd.LoadFloat32x4Array((*[4]float32)(x[i:]))
		yv := archsimd.LoadFloat32x4Array((*[4]float32)(y[i:]))
		left := midv.MulAdd(xv, yv.Neg())
		right := midv.MulAdd(xv, yv)
		lg.Mul(left).StoreArray((*[4]float32)(x[i:]))
		rg.Mul(right).StoreArray((*[4]float32)(y[i:]))
	}
	return n
}

//go:noinline
func stereoMergeRescaleAVX(x, y []float32, mid, lgain, rgain float32) int {
	n := len(x) &^ 3
	x = x[:n]
	y = y[:n]
	midv := broadcastF32x4Arch(mid)
	lg := broadcastF32x4Arch(lgain)
	rg := broadcastF32x4Arch(rgain)
	for i := 0; i < n; i += 4 {
		l := midv.Mul(archsimd.LoadFloat32x4Array((*[4]float32)(x[i:])))
		r := archsimd.LoadFloat32x4Array((*[4]float32)(y[i:]))
		lg.Mul(l.Sub(r)).StoreArray((*[4]float32)(x[i:]))
		rg.Mul(l.Add(r)).StoreArray((*[4]float32)(y[i:]))
	}
	return n
}
