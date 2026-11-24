//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// stereoSplitInto runs stereo_split() over x and y of equal length. The
// vector body follows the selected target's contraction order, and the
// remainder uses the matching scalar target path.
func stereoSplitInto(x, y []celtNorm) {
	y = y[:len(x)]
	if !archsimd.X86.AVX() {
		stereoSplitScalarTarget(x, y)
		return
	}
	stereoSplitIntoAVX(x, y)
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
