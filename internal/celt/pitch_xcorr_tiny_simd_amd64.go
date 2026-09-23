//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"simd/archsimd"
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
		s04.Add(s15).Add(s26.Add(s37)).StoreArray(out)
		if xcorrGroupHasNaN(out) {
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

func xcorrGroupHasNaN(values *[8]float32) bool {
	for _, value := range *values {
		bits := math.Float32bits(value)
		if bits&0x7fffffff > 0x7f800000 {
			return true
		}
	}
	return false
}
