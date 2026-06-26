//go:build arm64 && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// stereoMergeRescaleNEON applies the final mid/side rescale of stereoMerge over
// len(x) lanes in place (l=mid*x; x=lgain*(l-y); y=rgain*(l+y)), 4-wide via NEON.
// It loads/stores through raw pointers (loadF32x4/storeF32x4) to drop the slice
// bounds check on every access. Mul, Sub and Add are distinct archsimd ops — no
// fused multiply-add across separate results — so each lane keeps the two-rounding
// shape of the scalar noFMA32 reference and the hand asm, and the result is
// bit-exact. The caller passes len(y) == len(x), so the y walk stays in range.
func stereoMergeRescaleNEON(x, y []float32, mid, lgain, rgain float32) {
	n := len(x)
	if n <= 0 {
		return
	}
	_ = y[:n]
	midv := archsimd.BroadcastFloat32x4(mid)
	lv := archsimd.BroadcastFloat32x4(lgain)
	rv := archsimd.BroadcastFloat32x4(rgain)
	xp := unsafe.Pointer(unsafe.SliceData(x))
	yp := unsafe.Pointer(unsafe.SliceData(y))
	i := 0
	for ; i+8 <= n; i += 8 {
		stereoMergeBlock4(xp, yp, midv, lv, rv)
		stereoMergeBlock4(unsafe.Add(xp, 16), unsafe.Add(yp, 16), midv, lv, rv)
		xp = unsafe.Add(xp, 32)
		yp = unsafe.Add(yp, 32)
	}
	for ; i+4 <= n; i += 4 {
		stereoMergeBlock4(xp, yp, midv, lv, rv)
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

// stereoMergeBlock4 rescales one 4-lane block in place at xp/yp.
func stereoMergeBlock4(xp, yp unsafe.Pointer, midv, lv, rv archsimd.Float32x4) {
	xv := loadF32x4(xp)
	yv := loadF32x4(yp)
	l := midv.Mul(xv)
	storeF32x4(xp, lv.Mul(l.Sub(yv)))
	storeF32x4(yp, rv.Mul(l.Add(yv)))
}
