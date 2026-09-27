//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"unsafe"

	"simd/archsimd"
)

var stereoMergeUsesAVX = archsimd.X86.AVX()

// stereoMergeRescaleNEON is the final loop of libopus celt/bands.c
// stereo_merge, l = mid*x[j], x[j] = lgain*(l-y[j]), y[j] = rgain*(l+y[j]),
// which GCC vectorizes in the SSE build; four lanes per step compute the same
// separately rounded products and sums.
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	y = y[:n]
	i := 0
	if stereoMergeUsesAVX && n >= 4 {
		midv := archsimd.BroadcastFloat32x4(mid)
		lg := archsimd.BroadcastFloat32x4(lgain)
		rg := archsimd.BroadcastFloat32x4(rgain)
		xp := unsafe.Pointer(unsafe.SliceData(x))
		yp := unsafe.Pointer(unsafe.SliceData(y))
		for ; i+4 <= n; i += 4 {
			l := midv.Mul(loadF32x4(unsafe.Add(xp, 4*i)))
			r := loadF32x4(unsafe.Add(yp, 4*i))
			storeF32x4(unsafe.Add(xp, 4*i), lg.Mul(l.Sub(r)))
			storeF32x4(unsafe.Add(yp, 4*i), rg.Mul(l.Add(r)))
		}
	}
	for ; i < n; i++ {
		l := noFMA32Mul(mid, x[i])
		r := y[i]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l, r))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l, r))
	}
}
