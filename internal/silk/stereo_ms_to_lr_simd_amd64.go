//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

var silkStereoUsesAVX2 = archsimd.X86.AVX2()

// stereoSMLAWB8 is silk_SMLAWB(a, b, c) on eight lanes, with c already
// sign-extended from its low 16 bits.
func stereoSMLAWB8(a, b, c, lowMask archsimd.Int32x8) archsimd.Int32x8 {
	hi := b.ShiftAllRight(16).Mul(c)
	lo := b.And(lowMask).Mul(c).ShiftAllRight(16)
	return a.Add(hi.Add(lo))
}

// stereoSMLAWB4 is silk_SMLAWB(a, b, c) on four lanes, with c already
// sign-extended from its low 16 bits.
func stereoSMLAWB4(a, b, c, lowMask archsimd.Int32x4) archsimd.Int32x4 {
	hi := b.ShiftAllRight(16).Mul(c)
	lo := b.And(lowMask).Mul(c).ShiftAllRight(16)
	return a.Add(hi.Add(lo))
}

// stereoLoad4 loads four int16 samples starting at s as int32 lanes.
func stereoLoad4(s []int16) archsimd.Int32x4 {
	var tmp [8]int16
	copy(tmp[:4], s[:4])
	return archsimd.LoadInt16x8Array(&tmp).ExtendLo4ToInt32()
}

// stereoPredictSide adds the stereo prediction to side[n+1] for n in
// [from, to), advancing the Q13 predictors by delta before each sample, eight
// samples per AVX2 vector with a four-sample tail.
func stereoPredictSide(mid, side []int16, from, to int, pred0, pred1, delta0, delta1 int32) {
	n := from
	if silkStereoUsesAVX2 && to+2 <= len(mid) && to+1 <= len(side) {
		n, pred0, pred1 = stereoPredictSideAVX2(mid, side, from, to, pred0, pred1, delta0, delta1)
	}
	stereoPredictSideScalar(mid, side, n, to, pred0, pred1, delta0, delta1)
}

//go:noinline
func stereoPredictSideAVX2(mid, side []int16, from, to int, pred0, pred1, delta0, delta1 int32) (int, int32, int32) {
	n := from
	if n+8 <= to {
		steps8 := archsimd.LoadInt32x8Array(&[8]int32{1, 2, 3, 4, 5, 6, 7, 8})
		p0 := archsimd.BroadcastInt32x8(pred0).Add(steps8.Mul(archsimd.BroadcastInt32x8(delta0)))
		p1 := archsimd.BroadcastInt32x8(pred1).Add(steps8.Mul(archsimd.BroadcastInt32x8(delta1)))
		step0 := archsimd.BroadcastInt32x8(delta0 << 3)
		step1 := archsimd.BroadcastInt32x8(delta1 << 3)
		lowMask := archsimd.BroadcastInt32x8(0xFFFF)
		one := archsimd.BroadcastInt32x8(1)
		for ; n+8 <= to; n += 8 {
			m := mid[n : n+10]
			m0 := archsimd.LoadInt16x8Array((*[8]int16)(m[0:])).ExtendToInt32()
			m1 := archsimd.LoadInt16x8Array((*[8]int16)(m[1:])).ExtendToInt32()
			m2 := archsimd.LoadInt16x8Array((*[8]int16)(m[2:])).ExtendToInt32()
			sp := (*[8]int16)(side[n+1 : n+9])
			s1 := archsimd.LoadInt16x8Array(sp).ExtendToInt32()
			c0 := p0.ShiftAllLeft(16).ShiftAllRight(16)
			c1 := p1.ShiftAllLeft(16).ShiftAllRight(16)
			sum := m0.Add(m2).Add(m1.ShiftAllLeft(1)).ShiftAllLeft(9)
			sum = stereoSMLAWB8(s1.ShiftAllLeft(8), sum, c0, lowMask)
			sum = stereoSMLAWB8(sum, m1.ShiftAllLeft(11), c1, lowMask)
			res := sum.ShiftAllRight(7).Add(one).ShiftAllRight(1)
			res.GetLo().SaturateToInt16Concat(res.GetHi()).StoreArray(sp)
			p0 = p0.Add(step0)
			p1 = p1.Add(step1)
		}
		archsimd.ClearAVXUpperBits()
		k := int32(n - from)
		pred0 += k * delta0
		pred1 += k * delta1
	}
	if n+4 <= to {
		steps := archsimd.LoadInt32x4Array(&[4]int32{1, 2, 3, 4})
		p0 := archsimd.BroadcastInt32x4(pred0).Add(steps.Mul(archsimd.BroadcastInt32x4(delta0)))
		p1 := archsimd.BroadcastInt32x4(pred1).Add(steps.Mul(archsimd.BroadcastInt32x4(delta1)))
		lowMask := archsimd.BroadcastInt32x4(0xFFFF)
		one := archsimd.BroadcastInt32x4(1)
		m := mid[n : n+6]
		m0 := stereoLoad4(m[0:])
		m1 := stereoLoad4(m[1:])
		m2 := stereoLoad4(m[2:])
		sp := side[n+1 : n+5]
		s1 := stereoLoad4(sp)
		c0 := p0.ShiftAllLeft(16).ShiftAllRight(16)
		c1 := p1.ShiftAllLeft(16).ShiftAllRight(16)
		sum := m0.Add(m2).Add(m1.ShiftAllLeft(1)).ShiftAllLeft(9)
		sum = stereoSMLAWB4(s1.ShiftAllLeft(8), sum, c0, lowMask)
		sum = stereoSMLAWB4(sum, m1.ShiftAllLeft(11), c1, lowMask)
		res := sum.ShiftAllRight(7).Add(one).ShiftAllRight(1)
		var out [8]int16
		res.SaturateToInt16Concat(res).StoreArray(&out)
		copy(sp, out[:4])
		n += 4
		pred0 += 4 * delta0
		pred1 += 4 * delta1
	}
	return n, pred0, pred1
}

// stereoMidSideToLR converts mid/side to left/right in place with saturating
// 16-bit adds, L = SAT16(mid+side), R = SAT16(mid-side), sixteen samples per
// AVX2 vector with an eight-sample tail.
func stereoMidSideToLR(mid, side []int16) {
	n := 0
	if silkStereoUsesAVX2 && len(side) >= len(mid) {
		n = stereoMidSideToLRAVX2(mid, side)
	}
	stereoMidSideToLRScalar(mid[n:], side[n:])
}

//go:noinline
func stereoMidSideToLRAVX2(mid, side []int16) int {
	n := 0
	for ; n+16 <= len(mid); n += 16 {
		mp := (*[16]int16)(mid[n:])
		sp := (*[16]int16)(side[n:])
		m := archsimd.LoadInt16x16Array(mp)
		s := archsimd.LoadInt16x16Array(sp)
		m.AddSaturated(s).StoreArray(mp)
		m.SubSaturated(s).StoreArray(sp)
	}
	archsimd.ClearAVXUpperBits()
	for ; n+8 <= len(mid); n += 8 {
		mp := (*[8]int16)(mid[n:])
		sp := (*[8]int16)(side[n:])
		m := archsimd.LoadInt16x8Array(mp)
		s := archsimd.LoadInt16x8Array(sp)
		m.AddSaturated(s).StoreArray(mp)
		m.SubSaturated(s).StoreArray(sp)
	}
	return n
}
