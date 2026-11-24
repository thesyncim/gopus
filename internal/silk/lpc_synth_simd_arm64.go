//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

func synthesizeLPCOrder16Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	if subfrLength <= 0 {
		return
	}
	_ = A_Q12[15]
	presQ14 = presQ14[:subfrLength]
	pxq = pxq[:subfrLength]
	win := sLPC[:maxLPCOrder+subfrLength]

	coef0 := [4]int32{int32(A_Q12[15]), int32(A_Q12[14]), int32(A_Q12[13]), int32(A_Q12[12])}
	coef1 := [4]int32{int32(A_Q12[11]), int32(A_Q12[10]), int32(A_Q12[9]), int32(A_Q12[8])}
	coef2 := [4]int32{int32(A_Q12[7]), int32(A_Q12[6]), int32(A_Q12[5]), int32(A_Q12[4])}
	c0 := archsimd.LoadInt32x4Array(&coef0)
	c1 := archsimd.LoadInt32x4Array(&coef1)
	c2 := archsimd.LoadInt32x4Array(&coef2)

	// Keep the four most recent LPC state samples in scalar registers so the AR
	// recurrence never hits a store-to-load forwarding stall from a 16-byte SIMD
	// load overlapping the previous iteration's 4-byte store to sLPC[16+i].
	a3 := int64(A_Q12[3])
	a2 := int64(A_Q12[2])
	a1 := int64(A_Q12[1])
	a0 := int64(A_Q12[0])
	s0, s1, s2, s3 := win[12], win[13], win[14], win[15]

	for i, p := range presQ14 {
		h := (*[maxLPCOrder + 1]int32)(win[i:])
		v0 := archsimd.LoadInt32x4Array((*[4]int32)(h[0:4]))
		v1 := archsimd.LoadInt32x4Array((*[4]int32)(h[4:8]))
		v2 := archsimd.LoadInt32x4Array((*[4]int32)(h[8:12]))
		predProducts := synthesizeLPCOrder16RoundProductsQ16(v0, c0)
		predProducts = predProducts.Add(synthesizeLPCOrder16RoundProductsQ16(v1, c1))
		predProducts = predProducts.Add(synthesizeLPCOrder16RoundProductsQ16(v2, c2))
		pred := int32(maxLPCOrder>>1) + predProducts.TruncToInt32().ReduceSum() +
			int32((int64(s0)*a3)>>16) +
			int32((int64(s1)*a2)>>16) +
			int32((int64(s2)*a1)>>16) +
			int32((int64(s3)*a0)>>16)
		s := silkAddSat32(p, lShiftSAT32By4(pred))
		h[maxLPCOrder] = s
		s0, s1, s2, s3 = s1, s2, s3, s
	}

	scaleLPCOutputNEON(win[maxLPCOrder:], pxq, gainQ10)
}

func synthesizeLPCOrder10Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	if subfrLength <= 0 {
		return
	}
	_ = A_Q12[minLPCOrder-1]
	presQ14 = presQ14[:subfrLength]
	pxq = pxq[:subfrLength]
	win := sLPC[maxLPCOrder-minLPCOrder : maxLPCOrder+subfrLength]

	// Older taps 9..2 live in two NEON vectors; taps 1..0 stay in scalar
	// registers to avoid store-to-load forwarding stalls.
	coef0 := [4]int32{int32(A_Q12[9]), int32(A_Q12[8]), int32(A_Q12[7]), int32(A_Q12[6])}
	coef1 := [4]int32{int32(A_Q12[5]), int32(A_Q12[4]), int32(A_Q12[3]), int32(A_Q12[2])}
	c0 := archsimd.LoadInt32x4Array(&coef0)
	c1 := archsimd.LoadInt32x4Array(&coef1)
	a1 := int64(A_Q12[1])
	a0 := int64(A_Q12[0])
	s0, s1 := win[8], win[9]

	for i, p := range presQ14 {
		h := (*[minLPCOrder + 1]int32)(win[i:])
		v0 := archsimd.LoadInt32x4Array((*[4]int32)(h[0:4]))
		v1 := archsimd.LoadInt32x4Array((*[4]int32)(h[4:8]))
		predProducts := synthesizeLPCOrder16RoundProductsQ16(v0, c0).Add(synthesizeLPCOrder16RoundProductsQ16(v1, c1))
		pred := int32(minLPCOrder>>1) + predProducts.TruncToInt32().ReduceSum() +
			int32((int64(s0)*a1)>>16) +
			int32((int64(s1)*a0)>>16)
		s := silkAddSat32(p, lShiftSAT32By4(pred))
		h[minLPCOrder] = s
		s0, s1 = s1, s
	}

	scaleLPCOutputNEON(sLPC[maxLPCOrder:maxLPCOrder+subfrLength], pxq, gainQ10)
}

func scaleLPCOutputNEON(outState []int32, pxq []int16, gainQ10 int32) {
	n := len(pxq)
	outState = outState[:n]
	vGain := archsimd.BroadcastInt32x4(gainQ10)
	vOne := archsimd.BroadcastInt32x4(1)
	i := 0
	for ; i+4 <= n; i += 4 {
		sv := archsimd.LoadInt32x4Array((*[4]int32)(outState[i:]))
		lo := sv.MulWidenLo(vGain).ShiftAllRight(16).TruncToInt32()
		hi := sv.HiToLo().MulWidenLo(vGain).ShiftAllRight(16).TruncToInt32()
		p := lo.InterleaveLo(hi)
		packed := p.InterleaveLo(p.HiToLo())
		q := packed.ShiftAllRight(7).Add(vOne).ShiftAllRight(1).SaturateToInt16()
		q.StorePart(pxq[i : i+4])
	}
	for ; i < n; i++ {
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(outState[i], gainQ10), 8))
	}
}

func synthesizeLPCOrder16RoundProductsQ16(x, y archsimd.Int32x4) archsimd.Int64x2 {
	// Shift each product before summing to preserve silkSMLAWB's Q16 rounding.
	lo := x.MulWidenLo(y).ShiftAllRight(16)
	hi := x.HiToLo().MulWidenLo(y.HiToLo()).ShiftAllRight(16)
	return lo.Add(hi)
}
