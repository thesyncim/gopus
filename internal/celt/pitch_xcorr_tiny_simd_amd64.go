//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"simd/archsimd"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// pitchXCorrFloat32AVX2FMAOrderTiny computes eight short correlations at once.
// Each vector lane is an output pitch; the eight accumulators retain the AVX2
// per-sample lane order before the same horizontal reduction as the kernel.
// Each output block uses the same length+7 input window as the AVX8 kernel.
func pitchXCorrFloat32AVX2FMAOrderTiny(x, y, xcorr []float32, length, maxPitch int) {
	if maxPitch <= 0 {
		return
	}
	if !libopusFloatPitchXCorrUsesAVX2FMA() {
		pitchXCorrFloat32AVX2FMAOrderTinyScalar(x, y, xcorr, length, maxPitch)
		return
	}
	if maxPitch < 8 {
		// No eight-lag block: celt_pitch_xcorr_avx2 runs every lag through
		// celt_inner_prod. The AVX2 bodies hoist their zero vectors to entry,
		// so this path stays outside them and runs its SSE code clean.
		for pitch := range maxPitch {
			xcorr[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], length)
		}
		return
	}
	pitchXCorrFloat32AVX2FMAOrderTinyAVX2(x, y, xcorr, length, maxPitch)
}

//go:noinline
func pitchXCorrFloat32AVX2FMAOrderTinyAVX2(x, y, xcorr []float32, length, maxPitch int) {
	// Every path clears the upper register halves the 256-bit lanes leave
	// dirty before its scalar SSE tail and the return to the caller.
	if length == 5 {
		pitchXCorrFloat32AVX2FMAOrderTiny5(x, y, xcorr, maxPitch)
		return
	}
	if length == 10 {
		pitchXCorrFloat32AVX2FMAOrderTiny10(x, y, xcorr, maxPitch)
		return
	}
	avxLimit := maxPitch &^ 7
	for pitch := 0; pitch < avxLimit; pitch += 8 {
		var acc0, acc1, acc2, acc3, acc4, acc5, acc6, acc7 archsimd.Float32x8
		yBatch := y[pitch : pitch+length+7]
		for j := 0; j < length; j += 8 {
			if j < length {
				acc0 = archsimd.BroadcastFloat32x8(x[j]).MulAdd(
					archsimd.LoadFloat32x8Array((*[8]float32)(yBatch[j:])), acc0)
			}
			if j+1 < length {
				acc1 = archsimd.BroadcastFloat32x8(x[j+1]).MulAdd(
					archsimd.LoadFloat32x8Array((*[8]float32)(yBatch[j+1:])), acc1)
			}
			if j+2 < length {
				acc2 = archsimd.BroadcastFloat32x8(x[j+2]).MulAdd(
					archsimd.LoadFloat32x8Array((*[8]float32)(yBatch[j+2:])), acc2)
			}
			if j+3 < length {
				acc3 = archsimd.BroadcastFloat32x8(x[j+3]).MulAdd(
					archsimd.LoadFloat32x8Array((*[8]float32)(yBatch[j+3:])), acc3)
			}
			if j+4 < length {
				acc4 = archsimd.BroadcastFloat32x8(x[j+4]).MulAdd(
					archsimd.LoadFloat32x8Array((*[8]float32)(yBatch[j+4:])), acc4)
			}
			if j+5 < length {
				acc5 = archsimd.BroadcastFloat32x8(x[j+5]).MulAdd(
					archsimd.LoadFloat32x8Array((*[8]float32)(yBatch[j+5:])), acc5)
			}
			if j+6 < length {
				acc6 = archsimd.BroadcastFloat32x8(x[j+6]).MulAdd(
					archsimd.LoadFloat32x8Array((*[8]float32)(yBatch[j+6:])), acc6)
			}
			if j+7 < length {
				acc7 = archsimd.BroadcastFloat32x8(x[j+7]).MulAdd(
					archsimd.LoadFloat32x8Array((*[8]float32)(yBatch[j+7:])), acc7)
			}
		}
		if remaining := length & 7; remaining != 0 {
			// pitch_avx.c mask-loads the final block and executes every FMA.
			// An inactive +0*+0 lane turns a prior -0 accumulator into +0.
			var zero archsimd.Float32x8
			if remaining <= 1 {
				acc1 = zero.MulAdd(zero, acc1)
			}
			if remaining <= 2 {
				acc2 = zero.MulAdd(zero, acc2)
			}
			if remaining <= 3 {
				acc3 = zero.MulAdd(zero, acc3)
			}
			if remaining <= 4 {
				acc4 = zero.MulAdd(zero, acc4)
			}
			if remaining <= 5 {
				acc5 = zero.MulAdd(zero, acc5)
			}
			if remaining <= 6 {
				acc6 = zero.MulAdd(zero, acc6)
			}
			acc7 = zero.MulAdd(zero, acc7)
		}
		s04 := acc0.Add(acc4)
		s15 := acc1.Add(acc5)
		s26 := acc2.Add(acc6)
		s37 := acc3.Add(acc7)
		out := (*[8]float32)(xcorr[pitch : pitch+8])
		result := s04.Add(s15).Add(s26.Add(s37))
		result.StoreArray(out)
		if result.NotEqual(result).ToBits() != 0 {
			// SIMD horizontal adds can select a different NaN sign or payload
			// than the lane-ordered AVX kernel. Recompute only this group with
			// that kernel's exact short-length path.
			var exact [8]float32
			// Use an AVX store before replay to avoid legacy SSE with dirty YMM state.
			var exactZero archsimd.Float32x8
			exactZero.StoreArray(&exact)
			xcorrKernelAVX8(&x[0], &yBatch[0], &exact, length)
			copy(out[:], exact[:])
		}
	}
	archsimd.ClearAVXUpperBits()
	for pitch := avxLimit; pitch < maxPitch; pitch++ {
		xcorr[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], length)
	}
}

// pitchXCorrFloat32AVX2FMAOrderTiny10 spells out the ten-sample fine-search
// shape so each FMA keeps the kernel's sample-lane and reduction order.
func pitchXCorrFloat32AVX2FMAOrderTiny10(x, y, xcorr []float32, maxPitch int) {
	x10 := x[:10]
	if maxPitch == 10 {
		y19 := y[:19]
		_ = xcorr[9]
		var zero archsimd.Float32x8
		acc0 := archsimd.BroadcastFloat32x8(x10[0]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(y19[0:])), zero)
		acc1 := archsimd.BroadcastFloat32x8(x10[1]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(y19[1:])), zero)
		acc2 := archsimd.BroadcastFloat32x8(x10[2]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(y19[2:])), zero)
		acc3 := archsimd.BroadcastFloat32x8(x10[3]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(y19[3:])), zero)
		acc4 := archsimd.BroadcastFloat32x8(x10[4]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(y19[4:])), zero)
		acc5 := archsimd.BroadcastFloat32x8(x10[5]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(y19[5:])), zero)
		acc6 := archsimd.BroadcastFloat32x8(x10[6]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(y19[6:])), zero)
		acc7 := archsimd.BroadcastFloat32x8(x10[7]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(y19[7:])), zero)
		acc0 = archsimd.BroadcastFloat32x8(x10[8]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(y19[8:])), acc0)
		acc1 = archsimd.BroadcastFloat32x8(x10[9]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(y19[9:])), acc1)
		acc2 = zero.MulAdd(zero, acc2)
		acc3 = zero.MulAdd(zero, acc3)
		acc4 = zero.MulAdd(zero, acc4)
		acc5 = zero.MulAdd(zero, acc5)
		acc6 = zero.MulAdd(zero, acc6)
		acc7 = zero.MulAdd(zero, acc7)

		s04 := acc0.Add(acc4)
		s15 := acc1.Add(acc5)
		s26 := acc2.Add(acc6)
		s37 := acc3.Add(acc7)
		result := s04.Add(s15).Add(s26.Add(s37))
		out := (*[8]float32)(xcorr[:8])
		result.StoreArray(out)
		if result.NotEqual(result).ToBits() != 0 {
			// SIMD horizontal adds can select a different NaN sign or payload than
			// the lane-ordered AVX kernel. Recompute only this group with that order.
			xcorrKernelAVX8(&x10[0], &y19[0], out, 10)
		}

		archsimd.ClearAVXUpperBits()
		xcorr[8] = innerProdFloat32SSEOrder10(x10, y19[8:18])
		xcorr[9] = innerProdFloat32SSEOrder10(x10, y19[9:19])
		return
	}

	avxLimit := maxPitch &^ 7
	var zero archsimd.Float32x8
	for pitch := 0; pitch < avxLimit; pitch += 8 {
		yb := y[pitch : pitch+17]
		acc0 := archsimd.BroadcastFloat32x8(x10[0]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[0:])), zero)
		acc1 := archsimd.BroadcastFloat32x8(x10[1]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[1:])), zero)
		acc2 := archsimd.BroadcastFloat32x8(x10[2]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[2:])), zero)
		acc3 := archsimd.BroadcastFloat32x8(x10[3]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[3:])), zero)
		acc4 := archsimd.BroadcastFloat32x8(x10[4]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[4:])), zero)
		acc5 := archsimd.BroadcastFloat32x8(x10[5]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[5:])), zero)
		acc6 := archsimd.BroadcastFloat32x8(x10[6]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[6:])), zero)
		acc7 := archsimd.BroadcastFloat32x8(x10[7]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[7:])), zero)
		acc0 = archsimd.BroadcastFloat32x8(x10[8]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[8:])), acc0)
		acc1 = archsimd.BroadcastFloat32x8(x10[9]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[9:])), acc1)
		acc2 = zero.MulAdd(zero, acc2)
		acc3 = zero.MulAdd(zero, acc3)
		acc4 = zero.MulAdd(zero, acc4)
		acc5 = zero.MulAdd(zero, acc5)
		acc6 = zero.MulAdd(zero, acc6)
		acc7 = zero.MulAdd(zero, acc7)

		s04 := acc0.Add(acc4)
		s15 := acc1.Add(acc5)
		s26 := acc2.Add(acc6)
		s37 := acc3.Add(acc7)
		result := s04.Add(s15).Add(s26.Add(s37))
		out := (*[8]float32)(xcorr[pitch : pitch+8])
		result.StoreArray(out)
		if result.NotEqual(result).ToBits() != 0 {
			var exact [8]float32
			// Use an AVX store before replay to avoid legacy SSE with dirty YMM state.
			var exactZero archsimd.Float32x8
			exactZero.StoreArray(&exact)
			xcorrKernelAVX8(&x10[0], &y[pitch], &exact, 10)
			copy(out[:], exact[:])
		}
	}
	archsimd.ClearAVXUpperBits()
	for pitch := avxLimit; pitch < maxPitch; pitch++ {
		xcorr[pitch] = innerProdFloat32SSEOrder(x10, y[pitch:], 10)
	}
}

// innerProdFloat32SSEOrder10 preserves the four-accumulator SSE order for a
// ten-sample tail without slice setup or a dynamic loop.
func innerProdFloat32SSEOrder10(x, y []float32) float32 {
	_ = x[9]
	_ = y[9]
	acc0 := noFMA32Add(0, noFMA32Mul(x[0], y[0]))
	acc1 := noFMA32Add(0, noFMA32Mul(x[1], y[1]))
	acc2 := noFMA32Add(0, noFMA32Mul(x[2], y[2]))
	acc3 := noFMA32Add(0, noFMA32Mul(x[3], y[3]))
	acc0 = noFMA32Add(acc0, noFMA32Mul(x[4], y[4]))
	acc1 = noFMA32Add(acc1, noFMA32Mul(x[5], y[5]))
	acc2 = noFMA32Add(acc2, noFMA32Mul(x[6], y[6]))
	acc3 = noFMA32Add(acc3, noFMA32Mul(x[7], y[7]))
	sum := noFMA32Add(noFMA32Add(acc0, acc2), noFMA32Add(acc1, acc3))
	sum = pitchXcorrSSETailMAC32(sum, x[8], y[8])
	sum = pitchXcorrSSETailMAC32(sum, x[9], y[9])
	if sum != sum {
		return opusmath.PitchXcorrSSENaNReplay(x[:10], y[:10], 10)
	}
	return sum
}

func pitchXCorrFloat32AVX2FMAOrderTiny5(x, y, xcorr []float32, maxPitch int) {
	x0 := archsimd.BroadcastFloat32x8(x[0])
	x1 := archsimd.BroadcastFloat32x8(x[1])
	x2 := archsimd.BroadcastFloat32x8(x[2])
	x3 := archsimd.BroadcastFloat32x8(x[3])
	x4 := archsimd.BroadcastFloat32x8(x[4])
	var zero archsimd.Float32x8
	avxLimit := maxPitch &^ 7
	for pitch := 0; pitch < avxLimit; pitch += 8 {
		yb := y[pitch : pitch+12]
		acc0 := x0.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[0:])), zero)
		acc1 := x1.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[1:])), zero)
		acc2 := x2.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[2:])), zero)
		acc3 := x3.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[3:])), zero)
		acc4 := x4.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yb[4:])), zero)
		out := (*[8]float32)(xcorr[pitch : pitch+8])
		result := acc0.Add(acc4).Add(acc1.Add(zero)).Add(acc2.Add(zero).Add(acc3.Add(zero)))
		result.StoreArray(out)
		if result.NotEqual(result).ToBits() != 0 {
			var exact [8]float32
			// Use an AVX store before replay to avoid legacy SSE with dirty YMM state.
			var exactZero archsimd.Float32x8
			exactZero.StoreArray(&exact)
			xcorrKernelAVX8(&x[0], &y[pitch], &exact, 5)
			copy(out[:], exact[:])
		}
	}
	archsimd.ClearAVXUpperBits()
	for pitch := avxLimit; pitch < maxPitch; pitch++ {
		xcorr[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], 5)
	}
}
