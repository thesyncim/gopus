//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"unsafe"

	"simd/archsimd"
)

const combUsesSSE = true

// combFilterConstSSE is libopus celt/x86/pitch_sse.c comb_filter_const_sse
// over dst[from:to] (to-from a multiple of four): four outputs per step, the
// center tap added first and the two side-tap products summed before the
// final add. delay[i] is x[i-T-2], so delay[i..i+4] are the five taps of
// output i; delay must hold to+3 elements.
func combFilterConstSSE(dst, src, delay []celtSig, from, to int, g10, g11, g12 float32) {
	if to <= from {
		return
	}
	_ = dst[to-1]
	_ = src[to-1]
	_ = delay[to+3]
	g10v := archsimd.BroadcastFloat32x4(g10)
	g11v := archsimd.BroadcastFloat32x4(g11)
	g12v := archsimd.BroadcastFloat32x4(g12)
	dp := unsafe.Pointer(unsafe.SliceData(dst))
	sp := unsafe.Pointer(unsafe.SliceData(src))
	xp := unsafe.Pointer(unsafe.SliceData(delay))
	for i := from; i < to; i += 4 {
		x := unsafe.Add(xp, 4*i)
		x0 := loadF32x4(x)
		x1 := loadF32x4(unsafe.Add(x, 4))
		x2 := loadF32x4(unsafe.Add(x, 8))
		x3 := loadF32x4(unsafe.Add(x, 12))
		x4 := loadF32x4(unsafe.Add(x, 16))
		yi := loadF32x4(unsafe.Add(sp, 4*i)).Add(g10v.Mul(x2))
		yi2 := g11v.Mul(x3.Add(x1)).Add(g12v.Mul(x4.Add(x0)))
		storeF32x4(unsafe.Add(dp, 4*i), yi.Add(yi2))
	}
}
