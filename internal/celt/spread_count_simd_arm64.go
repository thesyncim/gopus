//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// spreadCountThresholds counts how many coefficients x[0..n-1] satisfy
// x[j]*x[j]*nf < threshold for three thresholds (0.25, 0.0625, 0.015625).
func spreadCountThresholds(x []celtNorm, n int, nf float32) (t0, t1, t2 int) {
	x = x[:n]
	blocks := n &^ 3
	if blocks > 0 {
		nf4 := archsimd.BroadcastFloat32x4(nf)
		c0 := archsimd.BroadcastFloat32x4(spreadThresholds[0])
		c1 := archsimd.BroadcastFloat32x4(spreadThresholds[1])
		c2 := archsimd.BroadcastFloat32x4(spreadThresholds[2])
		var n0, n1, n2 archsimd.Int32x4
		xb := x[:blocks]
		for j := 0; j < len(xb); j += 4 {
			v := archsimd.LoadFloat32x4Array((*[4]float32)(xb[j:]))
			x2N := v.Mul(v).Mul(nf4)
			n0 = n0.Sub(x2N.Less(c0).ToInt32x4())
			n1 = n1.Sub(x2N.Less(c1).ToInt32x4())
			n2 = n2.Sub(x2N.Less(c2).ToInt32x4())
		}
		t0 = int(n0.GetElem(0) + n0.GetElem(1) + n0.GetElem(2) + n0.GetElem(3))
		t1 = int(n1.GetElem(0) + n1.GetElem(1) + n1.GetElem(2) + n1.GetElem(3))
		t2 = int(n2.GetElem(0) + n2.GetElem(1) + n2.GetElem(2) + n2.GetElem(3))
	}
	r0, r1, r2 := spreadCountThresholdsScalar(x[blocks:], nf)
	return t0 + r0, t1 + r1, t2 + r2
}
