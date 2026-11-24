//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

var spreadCountUsesAVX2 = archsimd.X86.AVX2()

// spreadCountThresholds counts how many coefficients x[0..n-1] satisfy
// x[j]*x[j]*nf < threshold for three thresholds (0.25, 0.0625, 0.015625).
// Eight lanes on AVX2 (or four on AVX1) run the scalar per-coefficient
// products and comparisons; each lane's true comparison adds one to that
// threshold's count.
func spreadCountThresholds(x []celtNorm, n int, nf float32) (t0, t1, t2 int) {
	x = x[:n]
	if spreadCountUsesAVX2 && n >= 8 {
		return spreadCountThresholdsAVX2(x, n, nf)
	}
	if !archsimd.X86.AVX() {
		return spreadCountThresholdsScalar(x, nf)
	}
	return spreadCountThresholdsAVX(x, n, nf)
}

//go:noinline
func spreadCountThresholdsAVX2(x []celtNorm, n int, nf float32) (t0, t1, t2 int) {
	x = x[:n]
	nf8 := archsimd.BroadcastFloat32x8(nf)
	c0 := archsimd.BroadcastFloat32x8(spreadThresholds[0])
	c1 := archsimd.BroadcastFloat32x8(spreadThresholds[1])
	c2 := archsimd.BroadcastFloat32x8(spreadThresholds[2])
	var n0, n1, n2 archsimd.Int32x8
	j := 0
	for ; j+8 <= n; j += 8 {
		v := archsimd.LoadFloat32x8Array((*[8]float32)(x[j:]))
		x2N := v.Mul(v).Mul(nf8)
		n0 = n0.Sub(x2N.Less(c0).ToInt32x8())
		n1 = n1.Sub(x2N.Less(c1).ToInt32x8())
		n2 = n2.Sub(x2N.Less(c2).ToInt32x8())
	}
	s0 := n0.GetLo().Add(n0.GetHi())
	s1 := n1.GetLo().Add(n1.GetHi())
	s2 := n2.GetLo().Add(n2.GetHi())
	archsimd.ClearAVXUpperBits()
	if j+4 <= n {
		nf4 := broadcastF32x4Arch(nf)
		c04 := broadcastF32x4Arch(spreadThresholds[0])
		c14 := broadcastF32x4Arch(spreadThresholds[1])
		c24 := broadcastF32x4Arch(spreadThresholds[2])
		v := archsimd.LoadFloat32x4Array((*[4]float32)(x[j:]))
		x2N := v.Mul(v).Mul(nf4)
		s0 = s0.Sub(x2N.Less(c04).ToInt32x4())
		s1 = s1.Sub(x2N.Less(c14).ToInt32x4())
		s2 = s2.Sub(x2N.Less(c24).ToInt32x4())
		j += 4
	}
	t0 = int(s0.GetElem(0) + s0.GetElem(1) + s0.GetElem(2) + s0.GetElem(3))
	t1 = int(s1.GetElem(0) + s1.GetElem(1) + s1.GetElem(2) + s1.GetElem(3))
	t2 = int(s2.GetElem(0) + s2.GetElem(1) + s2.GetElem(2) + s2.GetElem(3))
	r0, r1, r2 := spreadCountThresholdsScalar(x[j:], nf)
	return t0 + r0, t1 + r1, t2 + r2
}

//go:noinline
func spreadCountThresholdsAVX(x []celtNorm, n int, nf float32) (t0, t1, t2 int) {
	x = x[:n]
	blocks := n &^ 3
	if blocks > 0 {
		nf4 := broadcastF32x4Arch(nf)
		c0 := broadcastF32x4Arch(spreadThresholds[0])
		c1 := broadcastF32x4Arch(spreadThresholds[1])
		c2 := broadcastF32x4Arch(spreadThresholds[2])
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
