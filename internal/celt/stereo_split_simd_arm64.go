//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// stereoSplitInto runs stereo_split() over x and y of equal length, four NEON
// lanes at a time, matching stereoSplitScalar bit-for-bit.
func stereoSplitInto(x, y []celtNorm) {
	y = y[:len(x)]
	blocks := len(x) &^ 3
	if blocks > 0 {
		c := archsimd.BroadcastFloat32x4(stereoSplitInvSqrt2)
		xb := x[:blocks]
		yb := y[:blocks]
		for j := 0; j < len(xb); j += 4 {
			xv := archsimd.LoadFloat32x4Array((*[4]float32)(xb[j:]))
			yv := archsimd.LoadFloat32x4Array((*[4]float32)(yb[j:]))
			l := xv.Mul(c)
			r := yv.Mul(c)
			l.Add(r).StoreArray((*[4]float32)(xb[j:]))
			r.Sub(l).StoreArray((*[4]float32)(yb[j:]))
		}
	}
	stereoSplitScalarTarget(x[blocks:], y[blocks:])
}
