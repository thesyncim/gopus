//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

var silkUseLPCSynthAVX2 = archsimd.X86.AVX2()

func synthesizeLPCOrder16Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	if subfrLength <= 0 {
		return
	}
	if !silkUseLPCSynthAVX2 {
		synthesizeLPCOrder16CoreScalar(sLPC, A_Q12, presQ14, pxq, gainQ10, subfrLength)
		return
	}
	synthesizeLPCOrder16CoreAVX2(sLPC, A_Q12, presQ14, pxq, gainQ10, subfrLength)
}

//go:noinline
func synthesizeLPCOrder16CoreAVX2(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	_ = A_Q12[15]
	presQ14 = presQ14[:subfrLength]
	pxq = pxq[:subfrLength]
	win := sLPC[:maxLPCOrder+subfrLength]

	// Pack older taps 15..8 into two AVX2 even-lane vectors and taps 7..4 into
	// two 128-bit even-lane vectors for VPMULDQ. Because truncating a signed
	// 64-bit product >> 16 to int32 discards bits [63:48], logical VPSRLQ 16
	// is bit-identical to arithmetic >> 16.
	cEven0Arr := [8]int32{int32(A_Q12[15]), 0, int32(A_Q12[13]), 0, int32(A_Q12[11]), 0, int32(A_Q12[9]), 0}
	cOdd0Arr := [8]int32{int32(A_Q12[14]), 0, int32(A_Q12[12]), 0, int32(A_Q12[10]), 0, int32(A_Q12[8]), 0}
	cEven1Arr := [4]int32{int32(A_Q12[7]), 0, int32(A_Q12[5]), 0}
	cOdd1Arr := [4]int32{int32(A_Q12[6]), 0, int32(A_Q12[4]), 0}
	cEven0 := archsimd.LoadInt32x8Array(&cEven0Arr)
	cOdd0 := archsimd.LoadInt32x8Array(&cOdd0Arr)
	cEven1 := archsimd.LoadInt32x4Array(&cEven1Arr)
	cOdd1 := archsimd.LoadInt32x4Array(&cOdd1Arr)

	// Keep the four most recent LPC state samples in scalar registers so the AR
	// recurrence never hits a store-to-load forwarding stall from a SIMD load
	// overlapping the previous iteration's 4-byte store to sLPC[16+i].
	a3 := int64(A_Q12[3])
	a2 := int64(A_Q12[2])
	a1 := int64(A_Q12[1])
	a0 := int64(A_Q12[0])
	s0, s1, s2, s3 := win[12], win[13], win[14], win[15]

	for i, p := range presQ14 {
		h := (*[maxLPCOrder + 1]int32)(win[i:])
		v0 := archsimd.LoadInt32x8Array((*[8]int32)(h[0:8]))
		v1 := archsimd.LoadInt32x4Array((*[4]int32)(h[8:12]))

		p0 := v0.MulWidenEven(cEven0).AsUint64x4().ShiftAllRight(16).AsInt32x8()
		p1 := v0.AsUint64x4().ShiftAllRight(32).AsInt32x8().MulWidenEven(cOdd0).AsUint64x4().ShiftAllRight(16).AsInt32x8()
		q0 := v1.MulWidenEven(cEven1).AsUint64x2().ShiftAllRight(16).AsInt32x4()
		q1 := v1.AsUint64x2().ShiftAllRight(32).AsInt32x4().MulWidenEven(cOdd1).AsUint64x2().ShiftAllRight(16).AsInt32x4()

		sum8 := p0.Add(p1)
		sum4 := sum8.GetLo().Add(sum8.GetHi()).Add(q0).Add(q1)
		pred := int32(maxLPCOrder>>1) + sum4.GetElem(0) + sum4.GetElem(2) +
			int32((int64(s0)*a3)>>16) +
			int32((int64(s1)*a2)>>16) +
			int32((int64(s2)*a1)>>16) +
			int32((int64(s3)*a0)>>16)
		s := silkAddSat32(p, lShiftSAT32By4(pred))
		h[maxLPCOrder] = s
		s0, s1, s2, s3 = s1, s2, s3, s
	}

	scaleLPCOutputAVX2(win[maxLPCOrder:], pxq, gainQ10)
}

func synthesizeLPCOrder10Core(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	if subfrLength <= 0 {
		return
	}
	if !silkUseLPCSynthAVX2 {
		synthesizeLPCOrder10CoreScalar(sLPC, A_Q12, presQ14, pxq, gainQ10, subfrLength)
		return
	}
	synthesizeLPCOrder10CoreAVX2(sLPC, A_Q12, presQ14, pxq, gainQ10, subfrLength)
}

//go:noinline
func synthesizeLPCOrder10CoreAVX2(sLPC []int32, A_Q12 []int16, presQ14 []int32, pxq []int16, gainQ10 int32, subfrLength int) {
	_ = A_Q12[minLPCOrder-1]
	presQ14 = presQ14[:subfrLength]
	pxq = pxq[:subfrLength]
	win := sLPC[maxLPCOrder-minLPCOrder : maxLPCOrder+subfrLength]

	// Older taps 9..2 fit in a single Int32x8 load; taps 1..0 stay in scalar
	// registers to avoid store-to-load forwarding stalls.
	cEvenArr := [8]int32{int32(A_Q12[9]), 0, int32(A_Q12[7]), 0, int32(A_Q12[5]), 0, int32(A_Q12[3]), 0}
	cOddArr := [8]int32{int32(A_Q12[8]), 0, int32(A_Q12[6]), 0, int32(A_Q12[4]), 0, int32(A_Q12[2]), 0}
	cEven := archsimd.LoadInt32x8Array(&cEvenArr)
	cOdd := archsimd.LoadInt32x8Array(&cOddArr)
	a1 := int64(A_Q12[1])
	a0 := int64(A_Q12[0])
	s0, s1 := win[8], win[9]

	for i, p := range presQ14 {
		h := (*[minLPCOrder + 1]int32)(win[i:])
		v := archsimd.LoadInt32x8Array((*[8]int32)(h[0:8]))
		p0 := v.MulWidenEven(cEven).AsUint64x4().ShiftAllRight(16).AsInt32x8()
		p1 := v.AsUint64x4().ShiftAllRight(32).AsInt32x8().MulWidenEven(cOdd).AsUint64x4().ShiftAllRight(16).AsInt32x8()
		sum8 := p0.Add(p1)
		sum4 := sum8.GetLo().Add(sum8.GetHi())
		pred := int32(minLPCOrder>>1) + sum4.GetElem(0) + sum4.GetElem(2) +
			int32((int64(s0)*a1)>>16) +
			int32((int64(s1)*a0)>>16)
		s := silkAddSat32(p, lShiftSAT32By4(pred))
		h[minLPCOrder] = s
		s0, s1 = s1, s
	}

	scaleLPCOutputAVX2(sLPC[maxLPCOrder:maxLPCOrder+subfrLength], pxq, gainQ10)
}

func scaleLPCOutputAVX2(outState []int32, pxq []int16, gainQ10 int32) {
	n := len(pxq)
	outState = outState[:n]
	vGain := archsimd.BroadcastInt32x8(gainQ10)
	vOne := archsimd.BroadcastInt32x8(1)
	maskLo32 := archsimd.BroadcastUint64x4(0xffffffff)
	i := 0
	for ; i+8 <= n; i += 8 {
		sv := archsimd.LoadInt32x8Array((*[8]int32)(outState[i:]))
		even := sv.MulWidenEven(vGain).AsUint64x4().ShiftAllRight(16).And(maskLo32).AsInt32x8()
		odd := sv.AsUint64x4().ShiftAllRight(32).AsInt32x8().MulWidenEven(vGain).AsUint64x4().ShiftAllRight(16).ShiftAllLeft(32).AsInt32x8()
		packed := even.Or(odd)
		q := packed.ShiftAllRight(7).Add(vOne).ShiftAllRight(1)
		q.GetLo().SaturateToInt16Concat(q.GetHi()).StoreArray((*[8]int16)(pxq[i:]))
	}
	archsimd.ClearAVXUpperBits()
	for ; i < n; i++ {
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(outState[i], gainQ10), 8))
	}
}
