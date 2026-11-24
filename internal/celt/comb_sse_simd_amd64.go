//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

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
	dst = dst[:to]
	src = src[:to]
	delay = delay[:to+4]
	if !archsimd.X86.AVX() {
		for i := from; i < to; i++ {
			dst[i] = combFilterConstSSEValue(src[i], g10, g11, g12, delay[i+2], delay[i+3], delay[i+1], delay[i+4], delay[i])
		}
		return
	}
	combFilterConstSSEAVX(dst, src, delay, from, to, g10, g11, g12)
}

//go:noinline
func combFilterConstSSEAVX(dst, src, delay []celtSig, from, to int, g10, g11, g12 float32) {
	g10v := broadcastF32x4Arch(g10)
	g11v := broadcastF32x4Arch(g11)
	g12v := broadcastF32x4Arch(g12)
	// As in the C kernel, only x[i-T+2..i-T+5] is loaded per step; the other
	// taps are shuffled from it and the previous step's load, which keeps the
	// in-place loads that hit recent stores to one per step.
	x0 := archsimd.LoadFloat32x4Array((*[4]float32)(delay[from:]))
	for i := from; i+4 <= to; i += 4 {
		x4 := archsimd.LoadFloat32x4Array((*[4]float32)(delay[i+4:]))
		x2 := x0.ConcatPermuteScalars(2, 3, 4, 5, x4)
		x1 := x0.ConcatPermuteScalars(1, 2, 5, 6, x2)
		x3 := x2.ConcatPermuteScalars(1, 2, 5, 6, x4)
		base := archsimd.LoadFloat32x4Array((*[4]float32)(src[i:]))
		var yi, yi2 archsimd.Float32x4
		if combTargetV3FMA {
			// GCC 13.3 contracts celt/x86/pitch_sse.c's intrinsic chain for
			// -march=x86-64-v3: the center tap is fused with the base, and the
			// outer side tap is fused with the inner side product.
			yi = x2.MulAdd(g10v, base)
			yi2 = x4.Add(x0).MulAdd(g12v, g11v.Mul(x3.Add(x1)))
		} else {
			yi = base.Add(g10v.Mul(x2))
			yi2 = g11v.Mul(x3.Add(x1)).Add(g12v.Mul(x4.Add(x0)))
		}
		yi.Add(yi2).StoreArray((*[4]float32)(dst[i:]))
		x0 = x4
	}
}

var combOverlapUsesAVX = archsimd.X86.AVX()

// combOverlapVector reports that combFilterOverlap has a vector kernel.
const combOverlapVector = true

// combOverlapMax bounds the overlap the prefilter cross-fade runs through
// combFilterOverlap.
const combOverlapMax = 240

// combFilterOverlap is the cross-faded part of libopus comb_filter over
// len(dst) outputs, in place: d0[k] and d1[k] are x[i-T0-2+k] and
// x[i-T1-2+k] for the first output i, and wsq holds window[i]^2. Four outputs
// per vector compute the scalar loop's exact per-output expression; the
// periods are at least COMBFILTER_MINPERIOD, so no lane reads an output of its
// own vector. The kernel stays on 128-bit vectors, as the libopus SSE decoder
// does, so it does not move the core to a lower AVX frequency license.
func combFilterOverlap(dst, d0, d1, wsq []float32, g00, g01, g02, g10, g11, g12 float32) {
	if combOverlapUsesAVX && len(dst) >= 4 {
		combFilterOverlapAVX(dst, d0, d1, wsq, g00, g01, g02, g10, g11, g12)
		return
	}
	combFilterOverlapScalar(dst, d0, d1, wsq, g00, g01, g02, g10, g11, g12)
}

//go:noinline
func combFilterOverlapAVX(dst, d0, d1, wsq []float32, g00, g01, g02, g10, g11, g12 float32) {
	n := len(dst)
	d0 = d0[:n+4]
	d1 = d1[:n+4]
	wsq = wsq[:n]
	one := broadcastF32x4Arch(1)
	vg00, vg01, vg02 := broadcastF32x4Arch(g00), broadcastF32x4Arch(g01), broadcastF32x4Arch(g02)
	vg10, vg11, vg12 := broadcastF32x4Arch(g10), broadcastF32x4Arch(g11), broadcastF32x4Arch(g12)
	i := 0
	for ; i+4 <= n; i += 4 {
		f := archsimd.LoadFloat32x4Array((*[4]float32)(wsq[i:]))
		a := d0[i : i+8 : i+8]
		b := d1[i : i+8 : i+8]
		oneMinus := one.Sub(f)
		c00, c01, c02 := oneMinus.Mul(vg00), oneMinus.Mul(vg01), oneMinus.Mul(vg02)
		c10, c11, c12 := f.Mul(vg10), f.Mul(vg11), f.Mul(vg12)
		d0c := archsimd.LoadFloat32x4Array((*[4]float32)(a[2:6]))
		d01 := archsimd.LoadFloat32x4Array((*[4]float32)(a[3:7])).Add(archsimd.LoadFloat32x4Array((*[4]float32)(a[1:5])))
		d02 := archsimd.LoadFloat32x4Array((*[4]float32)(a[4:8])).Add(archsimd.LoadFloat32x4Array((*[4]float32)(a[0:4])))
		d1c := archsimd.LoadFloat32x4Array((*[4]float32)(b[2:6]))
		d11 := archsimd.LoadFloat32x4Array((*[4]float32)(b[3:7])).Add(archsimd.LoadFloat32x4Array((*[4]float32)(b[1:5])))
		d12 := archsimd.LoadFloat32x4Array((*[4]float32)(b[4:8])).Add(archsimd.LoadFloat32x4Array((*[4]float32)(b[0:4])))
		sum := archsimd.LoadFloat32x4Array((*[4]float32)(dst[i:]))
		if combTargetV3FMA {
			// The x86-64-v3 C overlap path contracts each tap product into its
			// running sum while keeping tap gains and tap-pair sums rounded.
			sum = c00.MulAdd(d0c, sum)
			sum = c01.MulAdd(d01, sum)
			sum = c02.MulAdd(d02, sum)
			sum = c10.MulAdd(d1c, sum)
			sum = c11.MulAdd(d11, sum)
			sum = c12.MulAdd(d12, sum)
		} else {
			sum = sum.Add(c00.Mul(d0c)).
				Add(c01.Mul(d01)).
				Add(c02.Mul(d02)).
				Add(c10.Mul(d1c)).
				Add(c11.Mul(d11)).
				Add(c12.Mul(d12))
		}
		sum.StoreArray((*[4]float32)(dst[i:]))
	}
	if i < n {
		combFilterOverlapScalar(dst[i:], d0[i:], d1[i:], wsq[i:], g00, g01, g02, g10, g11, g12)
	}
}
