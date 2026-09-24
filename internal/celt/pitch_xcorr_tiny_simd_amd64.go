//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// pitchXCorrFloat32AVX2FMAOrderTiny computes eight short correlations at once.
// Each vector lane is an output pitch; the eight accumulators retain the AVX2
// per-sample lane order before the same horizontal reduction as the kernel.
// Each output block uses the same length+7 input window as the AVX8 kernel.
func pitchXCorrFloat32AVX2FMAOrderTiny(x, y, xcorr []float32, length, maxPitch int) {
	if maxPitch <= 0 {
		return
	}
	if !archsimd.X86.FMA() {
		pitchXCorrFloat32AVX2FMAOrderTinyScalar(x, y, xcorr, length, maxPitch)
		return
	}
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
			xcorrKernelAVX8(&x[0], &yBatch[0], &exact, length)
			copy(out[:], exact[:])
		}
	}
	for pitch := avxLimit; pitch < maxPitch; pitch++ {
		xcorr[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], length)
	}
}

// pitchXCorrFloat32AVX2FMAOrderTiny10 spells out the ten-sample fine-search
// shape so each FMA keeps the kernel's sample-lane and reduction order.
func pitchXCorrFloat32AVX2FMAOrderTiny10(x, y, xcorr []float32, maxPitch int) {
	avxLimit := maxPitch &^ 7
	if avxLimit == 0 {
		for pitch := 0; pitch < maxPitch; pitch++ {
			xcorr[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], 10)
		}
		return
	}

	var zero archsimd.Float32x8
	for pitch := 0; pitch < avxLimit; pitch += 8 {
		yp := unsafe.Pointer(unsafe.SliceData(y[pitch : pitch+17]))
		acc0 := archsimd.BroadcastFloat32x8(x[0]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yp)), zero)
		acc1 := archsimd.BroadcastFloat32x8(x[1]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 4))), zero)
		acc2 := archsimd.BroadcastFloat32x8(x[2]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 8))), zero)
		acc3 := archsimd.BroadcastFloat32x8(x[3]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 12))), zero)
		acc4 := archsimd.BroadcastFloat32x8(x[4]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 16))), zero)
		acc5 := archsimd.BroadcastFloat32x8(x[5]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 20))), zero)
		acc6 := archsimd.BroadcastFloat32x8(x[6]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 24))), zero)
		acc7 := archsimd.BroadcastFloat32x8(x[7]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 28))), zero)
		acc0 = archsimd.BroadcastFloat32x8(x[8]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 32))), acc0)
		acc1 = archsimd.BroadcastFloat32x8(x[9]).MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 36))), acc1)

		s04 := acc0.Add(acc4)
		s15 := acc1.Add(acc5)
		s26 := acc2.Add(acc6)
		s37 := acc3.Add(acc7)
		result := s04.Add(s15).Add(s26.Add(s37))
		out := (*[8]float32)(xcorr[pitch : pitch+8])
		result.StoreArray(out)
		if result.NotEqual(result).ToBits() != 0 {
			var exact [8]float32
			xcorrKernelAVX8(&x[0], &y[pitch], &exact, 10)
			copy(out[:], exact[:])
		}
	}
	for pitch := avxLimit; pitch < maxPitch; pitch++ {
		xcorr[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], 10)
	}
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
		yp := unsafe.Pointer(unsafe.SliceData(y[pitch : pitch+12]))
		acc0 := x0.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yp)), zero)
		acc1 := x1.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 4))), zero)
		acc2 := x2.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 8))), zero)
		acc3 := x3.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 12))), zero)
		acc4 := x4.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 16))), zero)
		out := (*[8]float32)(xcorr[pitch : pitch+8])
		result := acc0.Add(acc4).Add(acc1.Add(zero)).Add(acc2.Add(zero).Add(acc3.Add(zero)))
		result.StoreArray(out)
		if result.NotEqual(result).ToBits() != 0 {
			var exact [8]float32
			xcorrKernelAVX8(&x[0], &y[pitch], &exact, 5)
			copy(out[:], exact[:])
		}
	}
	for pitch := avxLimit; pitch < maxPitch; pitch++ {
		xcorr[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], 5)
	}
}
