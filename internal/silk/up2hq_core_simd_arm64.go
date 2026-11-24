//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

// up2HQCore is silk_resampler_private_up2_HQ (silk/resampler_private_up2_HQ.c).
// The even- and odd-sample allpass chains are independent, so lanes 0 and 1
// carry the even and odd chains of each allpass section in parallel using
// SMULL + SSHR + XTN, matching up2HQCoreGo bit-for-bit.
func up2HQCore(out []int16, in []int16, sIIR *[6]int32) {
	if len(in) == 0 {
		return
	}
	out = out[:2*len(in)]
	sa := archsimd.LoadInt32x4Array(&[4]int32{sIIR[0], sIIR[3], 0, 0})
	sb := archsimd.LoadInt32x4Array(&[4]int32{sIIR[1], sIIR[4], 0, 0})
	sc := archsimd.LoadInt32x4Array(&[4]int32{sIIR[2], sIIR[5], 0, 0})
	ca := archsimd.LoadInt32x4Array(&[4]int32{int32(up2HQ00), int32(up2HQ10), 0, 0})
	cb := archsimd.LoadInt32x4Array(&[4]int32{int32(up2HQ01), int32(up2HQ11), 0, 0})
	cc := archsimd.LoadInt32x4Array(&[4]int32{int32(up2HQ02), int32(up2HQ12), 0, 0})
	one := archsimd.BroadcastInt32x4(1)
	for k, x := range in {
		in32 := archsimd.BroadcastInt32x4(int32(x) << 10)

		// First all-pass section
		xv := up2HQMulQ16(in32.Sub(sa), ca)
		out1 := sa.Add(xv)
		sa = in32.Add(xv)

		// Second all-pass section
		xv = up2HQMulQ16(out1.Sub(sb), cb)
		out2 := sb.Add(xv)
		sb = out1.Add(xv)

		// Third all-pass section
		y := out2.Sub(sc)
		xv = y.Add(up2HQMulQ16(y, cc))
		out1 = sc.Add(xv)
		sc = out2.Add(xv)

		// silk_RSHIFT_ROUND(out32_1, 10), then silk_SAT16 in the narrowing pack.
		r := out1.ShiftAllRight(9).Add(one).ShiftAllRight(1)
		p := r.SaturateToInt16()
		pair := out[2*k : 2*k+2]
		pair[0] = p.GetElem(0)
		pair[1] = p.GetElem(1)
	}
	sIIR[0], sIIR[3] = sa.GetElem(0), sa.GetElem(1)
	sIIR[1], sIIR[4] = sb.GetElem(0), sb.GetElem(1)
	sIIR[2], sIIR[5] = sc.GetElem(0), sc.GetElem(1)
}

// up2HQMulQ16 returns (int64(y)*c)>>16 truncated to int32 in lanes 0 and 1.
func up2HQMulQ16(y, c archsimd.Int32x4) archsimd.Int32x4 {
	return y.MulWidenLo(c).ShiftAllRight(16).TruncToInt32()
}
