//go:build amd64 && goexperiment.simd && !nosimd

package silk

import (
	"simd/archsimd"
	"unsafe"
)

var silkLPCAnalysisF32UsesAVX2 = archsimd.X86.AVX2()

// lpcAnalysisFilterF32 is silk_LPC_analysis_filter_FLP. GCC -O3 vectorizes
// the libopus filter across output samples; this computes eight output
// samples per vector with the same per-sample multiply and add order,
// LPC_pred = s[-1]*c[0] + s[-2]*c[1] + ..., and no fused multiply-add.
func lpcAnalysisFilterF32(rLPC, predCoef, s []float32, length, order int) {
	if !silkLPCAnalysisF32UsesAVX2 {
		lpcAnalysisFilterF32Scalar(rLPC, predCoef, s, length, order)
		return
	}
	lpcAnalysisFilterF32AVX2(rLPC, predCoef, s, length, order)
}

//go:noinline
func lpcAnalysisFilterF32AVX2(rLPC, predCoef, s []float32, length, order int) {
	switch order {
	case 6, 8, 10, 12, 16:
	default:
		lpcAnalysisFilterF32Scalar(rLPC, predCoef, s, length, order)
		return
	}
	if order > length {
		return
	}
	rLPC = rLPC[:length]
	s = s[:length]
	predCoef = predCoef[:order]

	ix := order
	for ; ix+8 <= length; ix += 8 {
		// history points at s[ix-1]; tap k reads s[ix-1-k].
		history := unsafe.Pointer(&s[ix-1])
		pred := archsimd.LoadFloat32x8Array((*[8]float32)(history)).Mul(archsimd.BroadcastFloat32x8(predCoef[0]))
		for k := 1; k < order; k++ {
			tap := archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(history, -4*k)))
			pred = pred.Add(tap.Mul(archsimd.BroadcastFloat32x8(predCoef[k])))
		}
		in := archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Pointer(&s[ix])))
		in.Sub(pred).StoreArray((*[8]float32)(unsafe.Pointer(&rLPC[ix])))
	}
	// The 256-bit lanes leave the upper register halves dirty; clear them
	// before the scalar SSE tail and the caller's code.
	archsimd.ClearAVXUpperBits()
	for ; ix < length; ix++ {
		lpcPred := s[ix-1] * predCoef[0]
		for k := 1; k < order; k++ {
			lpcPred += s[ix-1-k] * predCoef[k]
		}
		rLPC[ix] = s[ix] - lpcPred
	}
	for i := range order {
		rLPC[i] = 0
	}
}
