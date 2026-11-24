//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var stereoSplitUsesAVX2 = archsimd.X86.AVX2() && (!stereoSplitUsesFMA || archsimd.X86.FMA())

// stereoSplitInto runs stereo_split() over x and y of equal length. The
// vector body follows the selected target's contraction order, and the
// remainder uses the matching scalar target path.
func stereoSplitInto(x, y []celtNorm) {
	y = y[:len(x)]
	if stereoSplitUsesAVX2 && len(x) >= 8 {
		stereoSplitIntoAVX2(x, y)
		return
	}
	if !archsimd.X86.AVX() {
		stereoSplitScalarTarget(x, y)
		return
	}
	stereoSplitIntoAVX(x, y)
}

//go:noinline
func stereoSplitIntoAVX2(x, y []celtNorm) {
	n := len(x)
	c := archsimd.BroadcastFloat32x8(stereoSplitInvSqrt2)
	j := 0
	for ; j+8 <= n; j += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(x[j:]))
		yv := archsimd.LoadFloat32x8Array((*[8]float32)(y[j:]))
		if stereoSplitUsesFMA {
			r := yv.Mul(c)
			xv.MulAdd(c, r).StoreArray((*[8]float32)(x[j:]))
			xv.MulAdd(c.Neg(), r).StoreArray((*[8]float32)(y[j:]))
		} else {
			l := xv.Mul(c)
			r := yv.Mul(c)
			l.Add(r).StoreArray((*[8]float32)(x[j:]))
			r.Sub(l).StoreArray((*[8]float32)(y[j:]))
		}
	}
	archsimd.ClearAVXUpperBits()
	if j+4 <= n {
		c4 := broadcastF32x4Arch(stereoSplitInvSqrt2)
		xv := archsimd.LoadFloat32x4Array((*[4]float32)(x[j:]))
		yv := archsimd.LoadFloat32x4Array((*[4]float32)(y[j:]))
		if stereoSplitUsesFMA {
			r := yv.Mul(c4)
			xv.MulAdd(c4, r).StoreArray((*[4]float32)(x[j:]))
			xv.MulAdd(c4.Neg(), r).StoreArray((*[4]float32)(y[j:]))
		} else {
			l := xv.Mul(c4)
			r := yv.Mul(c4)
			l.Add(r).StoreArray((*[4]float32)(x[j:]))
			r.Sub(l).StoreArray((*[4]float32)(y[j:]))
		}
		j += 4
	}
	stereoSplitScalarTarget(x[j:], y[j:])
}

//go:noinline
func stereoSplitIntoAVX(x, y []celtNorm) {
	blocks := len(x) &^ 3
	if blocks > 0 {
		c := broadcastF32x4Arch(stereoSplitInvSqrt2)
		xb := x[:blocks]
		yb := y[:blocks]
		for j := 0; j < len(xb); j += 4 {
			xv := archsimd.LoadFloat32x4Array((*[4]float32)(xb[j:]))
			yv := archsimd.LoadFloat32x4Array((*[4]float32)(yb[j:]))
			if stereoSplitUsesFMA {
				r := yv.Mul(c)
				xv.MulAdd(c, r).StoreArray((*[4]float32)(xb[j:]))
				xv.MulAdd(c.Neg(), r).StoreArray((*[4]float32)(yb[j:]))
			} else {
				l := xv.Mul(c)
				r := yv.Mul(c)
				l.Add(r).StoreArray((*[4]float32)(xb[j:]))
				r.Sub(l).StoreArray((*[4]float32)(yb[j:]))
			}
		}
	}
	stereoSplitScalarTarget(x[blocks:], y[blocks:])
}
