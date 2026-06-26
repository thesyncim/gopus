//go:build amd64 && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// stereoMergeRescaleNEON applies the final mid/side rescale of stereoMerge over
// len(x) lanes in place (l=mid*x; x=lgain*(l-y); y=rgain*(l+y)). On an AVX CPU it
// runs 8 lanes per step through the 256-bit Float32x8; the 128-bit Float32x4 path
// clears the remainder and carries CPUs without AVX. Loads/stores go through raw
// pointers (loadF32x8/loadF32x4) to drop the per-access slice bounds check. Mul,
// Sub and Add are distinct ops — no fused multiply-add across separate results —
// so each lane keeps the two-rounding shape of the scalar noFMA32 reference,
// bit-exact regardless of width. The caller passes len(y) == len(x).
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	if n <= 0 {
		return
	}
	_ = y[:n]
	xp := unsafe.Pointer(unsafe.SliceData(x))
	yp := unsafe.Pointer(unsafe.SliceData(y))
	i := 0
	if archsimd.X86.AVX() {
		midv := archsimd.BroadcastFloat32x8(mid)
		lv := archsimd.BroadcastFloat32x8(lgain)
		rv := archsimd.BroadcastFloat32x8(rgain)
		for ; i+8 <= n; i += 8 {
			xv := loadF32x8(xp)
			yv := loadF32x8(yp)
			l := midv.Mul(xv)
			storeF32x8(xp, lv.Mul(l.Sub(yv)))
			storeF32x8(yp, rv.Mul(l.Add(yv)))
			xp = unsafe.Add(xp, 32)
			yp = unsafe.Add(yp, 32)
		}
	}
	midv := archsimd.BroadcastFloat32x4(mid)
	lv := archsimd.BroadcastFloat32x4(lgain)
	rv := archsimd.BroadcastFloat32x4(rgain)
	for ; i+4 <= n; i += 4 {
		xv := loadF32x4(xp)
		yv := loadF32x4(yp)
		l := midv.Mul(xv)
		storeF32x4(xp, lv.Mul(l.Sub(yv)))
		storeF32x4(yp, rv.Mul(l.Add(yv)))
		xp = unsafe.Add(xp, 16)
		yp = unsafe.Add(yp, 16)
	}
	for ; i < n; i++ {
		xv := *(*float32)(xp)
		yv := *(*float32)(yp)
		l := noFMA32Mul(mid, xv)
		*(*float32)(xp) = noFMA32Mul(lgain, noFMA32Sub(l, yv))
		*(*float32)(yp) = noFMA32Mul(rgain, noFMA32Add(l, yv))
		xp = unsafe.Add(xp, 4)
		yp = unsafe.Add(yp, 4)
	}
}
