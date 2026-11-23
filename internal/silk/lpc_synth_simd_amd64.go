//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

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
	_ = sLPC[maxLPCOrder+subfrLength-1]
	_ = presQ14[subfrLength-1]
	_ = pxq[subfrLength-1]

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
	s0, s1, s2, s3 := sLPC[12], sLPC[13], sLPC[14], sLPC[15]

	stateBase := unsafe.Pointer(unsafe.SliceData(sLPC))
	for i := 0; i < subfrLength; i++ {
		off := i * 4
		v0 := archsimd.LoadInt32x8Array((*[8]int32)(unsafe.Add(stateBase, off)))
		v1 := archsimd.LoadInt32x4Array((*[4]int32)(unsafe.Add(stateBase, off+32)))

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
		s := silkAddSat32(presQ14[i], lShiftSAT32By4(pred))
		*(*int32)(unsafe.Add(stateBase, (maxLPCOrder+i)*4)) = s
		s0, s1, s2, s3 = s1, s2, s3, s
	}

	// Output scaling + rounding + saturation is independent of the AR loop and
	// runs 8-wide in AVX2 over sLPC[16 : 16+subfrLength].
	outState := unsafe.Add(stateBase, maxLPCOrder*4)
	pxqBase := unsafe.Pointer(unsafe.SliceData(pxq))
	vGain := archsimd.BroadcastInt32x8(gainQ10)
	vOne := archsimd.BroadcastInt32x8(1)
	maskLo32 := archsimd.BroadcastUint64x4(0xffffffff)
	i := 0
	for ; i+8 <= subfrLength; i += 8 {
		sv := archsimd.LoadInt32x8Array((*[8]int32)(unsafe.Add(outState, i*4)))
		even := sv.MulWidenEven(vGain).AsUint64x4().ShiftAllRight(16).And(maskLo32).AsInt32x8()
		odd := sv.AsUint64x4().ShiftAllRight(32).AsInt32x8().MulWidenEven(vGain).AsUint64x4().ShiftAllRight(16).ShiftAllLeft(32).AsInt32x8()
		packed := even.Or(odd)
		q := packed.ShiftAllRight(7).Add(vOne).ShiftAllRight(1)
		q.GetLo().SaturateToInt16Concat(q.GetHi()).StoreArray((*[8]int16)(unsafe.Add(pxqBase, i*2)))
	}
	archsimd.ClearAVXUpperBits()
	for ; i < subfrLength; i++ {
		s := *(*int32)(unsafe.Add(outState, i*4))
		pxq[i] = silkSAT16(silkRSHIFT_ROUND(silkSMULWW(s, gainQ10), 8))
	}
}
