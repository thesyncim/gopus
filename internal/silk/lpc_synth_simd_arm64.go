//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

func synthesizeLPCOrder16Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	_ = A_Q12[15]
	_ = sLPC[maxLPCOrder-1]
	if subfrLength <= 0 {
		return
	}
	_ = sLPC[maxLPCOrder+subfrLength-1]
	_ = presQ14[subfrLength-1]
	_ = pxq[subfrLength-1]

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
	s0, s1, s2, s3 := sLPC[12], sLPC[13], sLPC[14], sLPC[15]

	// The final checked state index is 15+subfrLength. Each iteration loads
	// i..i+11 (always >= 5 samples old) and stores at 16+i.
	stateBase := unsafe.Pointer(unsafe.SliceData(sLPC))
	for i := 0; i < subfrLength; i++ {
		stateOffset := i * 4
		v0 := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(stateBase, stateOffset)))
		v1 := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(stateBase, stateOffset+16)))
		v2 := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(stateBase, stateOffset+32)))
		// The rounded products fit int32 individually. Summing them in int64
		// avoids horizontal reductions; the final narrowing preserves int32 wrap.
		predProducts := synthesizeLPCOrder16RoundProductsQ16(v0, c0)
		predProducts = predProducts.Add(synthesizeLPCOrder16RoundProductsQ16(v1, c1))
		predProducts = predProducts.Add(synthesizeLPCOrder16RoundProductsQ16(v2, c2))
		pred := int32(maxLPCOrder>>1) + predProducts.TruncToInt32().ReduceSum() +
			int32((int64(s0)*a3)>>16) +
			int32((int64(s1)*a2)>>16) +
			int32((int64(s2)*a1)>>16) +
			int32((int64(s3)*a0)>>16)
		s := silkAddSat32(presQ14[i], lShiftSAT32By4(pred))
		*(*int32)(unsafe.Add(stateBase, (maxLPCOrder+i)*4)) = s
		s0, s1, s2, s3 = s1, s2, s3, s
	}

	// Output scaling + rounding + saturation is independent of the AR loop and
	// runs 4-wide in NEON over sLPC[16 : 16+subfrLength].
	scaleLPCOutputNEON(unsafe.Add(stateBase, maxLPCOrder*4), pxq, gainQ10, subfrLength)
}

func synthesizeLPCOrder10Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	if subfrLength <= 0 {
		return
	}
	_ = A_Q12[minLPCOrder-1]
	_ = sLPC[maxLPCOrder+subfrLength-1]
	_ = presQ14[subfrLength-1]
	_ = pxq[subfrLength-1]

	// Older taps 9..2 live in two NEON vectors; taps 1..0 stay in scalar
	// registers to avoid store-to-load forwarding stalls.
	coef0 := [4]int32{int32(A_Q12[9]), int32(A_Q12[8]), int32(A_Q12[7]), int32(A_Q12[6])}
	coef1 := [4]int32{int32(A_Q12[5]), int32(A_Q12[4]), int32(A_Q12[3]), int32(A_Q12[2])}
	c0 := archsimd.LoadInt32x4Array(&coef0)
	c1 := archsimd.LoadInt32x4Array(&coef1)
	a1 := int64(A_Q12[1])
	a0 := int64(A_Q12[0])
	s0, s1 := sLPC[14], sLPC[15]

	stateBase := unsafe.Pointer(unsafe.SliceData(sLPC))
	winBase := unsafe.Add(stateBase, (maxLPCOrder-minLPCOrder)*4)
	for i := 0; i < subfrLength; i++ {
		off := i * 4
		v0 := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(winBase, off)))
		v1 := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(winBase, off+16)))
		predProducts := synthesizeLPCOrder16RoundProductsQ16(v0, c0).Add(synthesizeLPCOrder16RoundProductsQ16(v1, c1))
		pred := int32(minLPCOrder>>1) + predProducts.TruncToInt32().ReduceSum() +
			int32((int64(s0)*a1)>>16) +
			int32((int64(s1)*a0)>>16)
		s := silkAddSat32(presQ14[i], lShiftSAT32By4(pred))
		*(*int32)(unsafe.Add(stateBase, (maxLPCOrder+i)*4)) = s
		s0, s1 = s1, s
	}

	scaleLPCOutputNEON(unsafe.Add(stateBase, maxLPCOrder*4), pxq, gainQ10, subfrLength)
}

func scaleLPCOutputNEON(outState unsafe.Pointer, pxq []int16, gainQ10 int32, subfrLength int) {
	vGain := archsimd.BroadcastInt32x4(gainQ10)
	vOne := archsimd.BroadcastInt32x4(1)
	i := 0
	for ; i+4 <= subfrLength; i += 4 {
		sv := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(outState, i*4)))
		lo := sv.MulWidenLo(vGain).ShiftAllRight(16).TruncToInt32()
		hi := sv.HiToLo().MulWidenLo(vGain).ShiftAllRight(16).TruncToInt32()
		p := lo.InterleaveLo(hi)
		packed := p.InterleaveLo(p.HiToLo())
		q := packed.ShiftAllRight(7).Add(vOne).ShiftAllRight(1).SaturateToInt16()
		q.StorePart(pxq[i : i+4])
	}
	for ; i < subfrLength; i++ {
		s := *(*int32)(unsafe.Add(outState, i*4))
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(s, gainQ10), 8))
	}
}

func synthesizeLPCOrder16RoundProductsQ16(x, y archsimd.Int32x4) archsimd.Int64x2 {
	// Shift each product before summing to preserve silkSMLAWB's Q16 rounding.
	lo := x.MulWidenLo(y).ShiftAllRight(16)
	hi := x.HiToLo().MulWidenLo(y.HiToLo()).ShiftAllRight(16)
	return lo.Add(hi)
}
