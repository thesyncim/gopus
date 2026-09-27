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
	// As in the C kernel, only x[i-T+2..i-T+5] is loaded per step; the other
	// taps are shuffled from it and the previous step's load, which keeps the
	// in-place loads that hit recent stores to one per step.
	x0 := loadF32x4(unsafe.Add(xp, 4*from))
	for i := from; i < to; i += 4 {
		x4 := loadF32x4(unsafe.Add(xp, 4*(i+4)))
		x2 := x0.ConcatPermuteScalars(2, 3, 4, 5, x4)
		x1 := x0.ConcatPermuteScalars(1, 2, 5, 6, x2)
		x3 := x2.ConcatPermuteScalars(1, 2, 5, 6, x4)
		yi := loadF32x4(unsafe.Add(sp, 4*i)).Add(g10v.Mul(x2))
		yi2 := g11v.Mul(x3.Add(x1)).Add(g12v.Mul(x4.Add(x0)))
		storeF32x4(unsafe.Add(dp, 4*i), yi.Add(yi2))
		x0 = x4
	}
}

var combOverlapUsesAVX = archsimd.X86.AVX()

// combFilterOverlap is the cross-faded part of libopus comb_filter over
// len(dst) outputs, in place: d0[k] and d1[k] are x[i-T0-2+k] and
// x[i-T1-2+k] for the first output i, and wsq holds window[i]^2. Four outputs
// per vector compute the scalar loop's exact per-output expression; the
// periods are at least COMBFILTER_MINPERIOD, so no lane reads an output of its
// own vector. The kernel stays on 128-bit vectors, as the libopus SSE decoder
// does, so it does not move the core to a lower AVX frequency license.
func combFilterOverlap(dst, d0, d1, wsq []float32, g00, g01, g02, g10, g11, g12 float32) {
	n := len(dst)
	i := 0
	if combOverlapUsesAVX && n >= 4 {
		_ = d0[n+3]
		_ = d1[n+3]
		_ = wsq[n-1]
		one := archsimd.BroadcastFloat32x4(1)
		vg00, vg01, vg02 := archsimd.BroadcastFloat32x4(g00), archsimd.BroadcastFloat32x4(g01), archsimd.BroadcastFloat32x4(g02)
		vg10, vg11, vg12 := archsimd.BroadcastFloat32x4(g10), archsimd.BroadcastFloat32x4(g11), archsimd.BroadcastFloat32x4(g12)
		dp := unsafe.Pointer(unsafe.SliceData(dst))
		ap := unsafe.Pointer(unsafe.SliceData(d0))
		bp := unsafe.Pointer(unsafe.SliceData(d1))
		wp := unsafe.Pointer(unsafe.SliceData(wsq))
		for ; i+4 <= n; i += 4 {
			f := loadF32x4(unsafe.Add(wp, 4*i))
			a := unsafe.Add(ap, 4*i)
			b := unsafe.Add(bp, 4*i)
			oneMinus := one.Sub(f)
			sum := loadF32x4(unsafe.Add(dp, 4*i)).
				Add(oneMinus.Mul(vg00).Mul(loadF32x4(unsafe.Add(a, 8)))).
				Add(oneMinus.Mul(vg01).Mul(loadF32x4(unsafe.Add(a, 12)).Add(loadF32x4(unsafe.Add(a, 4))))).
				Add(oneMinus.Mul(vg02).Mul(loadF32x4(unsafe.Add(a, 16)).Add(loadF32x4(a)))).
				Add(f.Mul(vg10).Mul(loadF32x4(unsafe.Add(b, 8)))).
				Add(f.Mul(vg11).Mul(loadF32x4(unsafe.Add(b, 12)).Add(loadF32x4(unsafe.Add(b, 4))))).
				Add(f.Mul(vg12).Mul(loadF32x4(unsafe.Add(b, 16)).Add(loadF32x4(b))))
			storeF32x4(unsafe.Add(dp, 4*i), sum)
		}
	}
	if i < n {
		combFilterOverlapScalar(dst[i:], d0[i:], d1[i:], wsq[i:], g00, g01, g02, g10, g11, g12)
	}
}
