//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

const combUsesSSE = true

var combUsesAVX2 = archsimd.X86.AVX2() && (!combTargetV3FMA || archsimd.X86.FMA())

// combFilterConstSSE is libopus celt/x86/pitch_sse.c comb_filter_const_sse
// over dst[from:to] (to-from a multiple of four): the center tap is added
// first and the two side-tap products are summed before the final add.
// delay[i] is x[i-T-2], so delay[i..i+4] are the five taps of output i; delay
// must hold to+4 elements. Because T >= COMBFILTER_MINPERIOD (15), an 8-wide
// AVX2 block never overlaps its own delay reads even when dst aliases x.
func combFilterConstSSE(dst, src, delay []celtSig, from, to int, g10, g11, g12 float32) {
	if to <= from {
		return
	}
	dst = dst[:to]
	src = src[:to]
	delay = delay[:to+4]
	if combUsesAVX2 && to-from >= 8 {
		combFilterConstSSEAVX2(dst, src, delay, from, to, g10, g11, g12)
		return
	}
	if !archsimd.X86.AVX() {
		for i := from; i < to; i++ {
			dst[i] = combFilterConstSSEValue(src[i], g10, g11, g12, delay[i+2], delay[i+3], delay[i+1], delay[i+4], delay[i])
		}
		return
	}
	combFilterConstSSEAVX(dst, src, delay, from, to, g10, g11, g12)
}

//go:noinline
func combFilterConstSSEAVX2(dst, src, delay []celtSig, from, to int, g10, g11, g12 float32) {
	g10v := archsimd.BroadcastFloat32x8(g10)
	g11v := archsimd.BroadcastFloat32x8(g11)
	g12v := archsimd.BroadcastFloat32x8(g12)
	i := from
	for ; i+8 <= to; i += 8 {
		d := delay[i : i+12 : i+12]
		x0 := archsimd.LoadFloat32x8Array((*[8]float32)(d[0:8]))
		x1 := archsimd.LoadFloat32x8Array((*[8]float32)(d[1:9]))
		x2 := archsimd.LoadFloat32x8Array((*[8]float32)(d[2:10]))
		x3 := archsimd.LoadFloat32x8Array((*[8]float32)(d[3:11]))
		x4 := archsimd.LoadFloat32x8Array((*[8]float32)(d[4:12]))
		base := archsimd.LoadFloat32x8Array((*[8]float32)(src[i:]))
		var yi, yi2 archsimd.Float32x8
		if combTargetV3FMA {
			yi = x2.MulAdd(g10v, base)
			yi2 = x4.Add(x0).MulAdd(g12v, g11v.Mul(x3.Add(x1)))
		} else {
			yi = base.Add(g10v.Mul(x2))
			yi2 = g11v.Mul(x3.Add(x1)).Add(g12v.Mul(x4.Add(x0)))
		}
		yi.Add(yi2).StoreArray((*[8]float32)(dst[i:]))
	}
	archsimd.ClearAVXUpperBits()
	if i+4 <= to {
		g10v4 := broadcastF32x4Arch(g10)
		g11v4 := broadcastF32x4Arch(g11)
		g12v4 := broadcastF32x4Arch(g12)
		d := delay[i : i+8 : i+8]
		x0 := archsimd.LoadFloat32x4Array((*[4]float32)(d[0:4]))
		x1 := archsimd.LoadFloat32x4Array((*[4]float32)(d[1:5]))
		x2 := archsimd.LoadFloat32x4Array((*[4]float32)(d[2:6]))
		x3 := archsimd.LoadFloat32x4Array((*[4]float32)(d[3:7]))
		x4 := archsimd.LoadFloat32x4Array((*[4]float32)(d[4:8]))
		base := archsimd.LoadFloat32x4Array((*[4]float32)(src[i:]))
		var yi, yi2 archsimd.Float32x4
		if combTargetV3FMA {
			yi = x2.MulAdd(g10v4, base)
			yi2 = x4.Add(x0).MulAdd(g12v4, g11v4.Mul(x3.Add(x1)))
		} else {
			yi = base.Add(g10v4.Mul(x2))
			yi2 = g11v4.Mul(x3.Add(x1)).Add(g12v4.Mul(x4.Add(x0)))
		}
		yi.Add(yi2).StoreArray((*[4]float32)(dst[i:]))
	}
}

//go:noinline
func combFilterConstSSEAVX(dst, src, delay []celtSig, from, to int, g10, g11, g12 float32) {
	g10v := broadcastF32x4Arch(g10)
	g11v := broadcastF32x4Arch(g11)
	g12v := broadcastF32x4Arch(g12)
	x0 := archsimd.LoadFloat32x4Array((*[4]float32)(delay[from:]))
	for i := from; i+4 <= to; i += 4 {
		x4 := archsimd.LoadFloat32x4Array((*[4]float32)(delay[i+4:]))
		x2 := x0.ConcatPermuteScalars(2, 3, 4, 5, x4)
		x1 := x0.ConcatPermuteScalars(1, 2, 5, 6, x2)
		x3 := x2.ConcatPermuteScalars(1, 2, 5, 6, x4)
		base := archsimd.LoadFloat32x4Array((*[4]float32)(src[i:]))
		var yi, yi2 archsimd.Float32x4
		if combTargetV3FMA {
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
// x[i-T1-2+k] for the first output i, and wsq holds window[i]^2.
func combFilterOverlap(dst, d0, d1, wsq []float32, g00, g01, g02, g10, g11, g12 float32) {
	if combUsesAVX2 && len(dst) >= 8 {
		combFilterOverlapAVX2(dst, d0, d1, wsq, g00, g01, g02, g10, g11, g12)
		return
	}
	if combOverlapUsesAVX && len(dst) >= 4 {
		combFilterOverlapAVX(dst, d0, d1, wsq, g00, g01, g02, g10, g11, g12)
		return
	}
	combFilterOverlapScalar(dst, d0, d1, wsq, g00, g01, g02, g10, g11, g12)
}

//go:noinline
func combFilterOverlapAVX2(dst, d0, d1, wsq []float32, g00, g01, g02, g10, g11, g12 float32) {
	n := len(dst)
	d0 = d0[:n+4]
	d1 = d1[:n+4]
	wsq = wsq[:n]
	one := archsimd.BroadcastFloat32x8(1)
	vg00, vg01, vg02 := archsimd.BroadcastFloat32x8(g00), archsimd.BroadcastFloat32x8(g01), archsimd.BroadcastFloat32x8(g02)
	vg10, vg11, vg12 := archsimd.BroadcastFloat32x8(g10), archsimd.BroadcastFloat32x8(g11), archsimd.BroadcastFloat32x8(g12)
	i := 0
	for ; i+8 <= n; i += 8 {
		f := archsimd.LoadFloat32x8Array((*[8]float32)(wsq[i:]))
		a := d0[i : i+12 : i+12]
		b := d1[i : i+12 : i+12]
		oneMinus := one.Sub(f)
		c00, c01, c02 := oneMinus.Mul(vg00), oneMinus.Mul(vg01), oneMinus.Mul(vg02)
		c10, c11, c12 := f.Mul(vg10), f.Mul(vg11), f.Mul(vg12)
		d0c := archsimd.LoadFloat32x8Array((*[8]float32)(a[2:10]))
		d01 := archsimd.LoadFloat32x8Array((*[8]float32)(a[3:11])).Add(archsimd.LoadFloat32x8Array((*[8]float32)(a[1:9])))
		d02 := archsimd.LoadFloat32x8Array((*[8]float32)(a[4:12])).Add(archsimd.LoadFloat32x8Array((*[8]float32)(a[0:8])))
		d1c := archsimd.LoadFloat32x8Array((*[8]float32)(b[2:10]))
		d11 := archsimd.LoadFloat32x8Array((*[8]float32)(b[3:11])).Add(archsimd.LoadFloat32x8Array((*[8]float32)(b[1:9])))
		d12 := archsimd.LoadFloat32x8Array((*[8]float32)(b[4:12])).Add(archsimd.LoadFloat32x8Array((*[8]float32)(b[0:8])))
		sum := archsimd.LoadFloat32x8Array((*[8]float32)(dst[i:]))
		if combTargetV3FMA {
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
		sum.StoreArray((*[8]float32)(dst[i:]))
	}
	archsimd.ClearAVXUpperBits()
	if i < n {
		combFilterOverlapAVX(dst[i:], d0[i:], d1[i:], wsq[i:], g00, g01, g02, g10, g11, g12)
	}
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
