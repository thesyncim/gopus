//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

// lpcAnalysisFilterF32 is silk_LPC_analysis_filter_FLP vectorized four output
// samples per ARM64 NEON step, matching lpcAnalysisFilterF32Scalar bit-for-bit.
func lpcAnalysisFilterF32(rLPC, predCoef, s []float32, length, order int) {
	switch order {
	case 6, 8, 10, 12, 16:
	default:
		lpcAnalysisFilterF32Scalar(rLPC, predCoef, s, length, order)
		return
	}
	if order > length {
		return
	}
	rLPC = rLPC[:length:length]
	s = s[:length:length]
	predCoef = predCoef[:order:order]

	ix := order
	for ; ix+4 <= length; ix += 4 {
		// Match libopus/Go arm64 FMA order: tap 1 is rounded first, then tap 0
		// and taps 2..order-1 accumulate via FMLA.
		pred := archsimd.LoadFloat32x4Array((*[4]float32)(s[ix-2 : ix+2])).Mul(archsimd.BroadcastFloat32x4(predCoef[1]))
		pred = archsimd.LoadFloat32x4Array((*[4]float32)(s[ix-1 : ix+3])).MulAdd(archsimd.BroadcastFloat32x4(predCoef[0]), pred)
		for k := 2; k < order; k++ {
			tap := archsimd.LoadFloat32x4Array((*[4]float32)(s[ix-1-k : ix+3-k]))
			pred = tap.MulAdd(archsimd.BroadcastFloat32x4(predCoef[k]), pred)
		}
		in := archsimd.LoadFloat32x4Array((*[4]float32)(s[ix : ix+4]))
		in.Sub(pred).StoreArray((*[4]float32)(rLPC[ix : ix+4]))
	}
	for ; ix < length; ix++ {
		lpcPred := round32(s[ix-2] * predCoef[1])
		lpcPred += s[ix-1] * predCoef[0]
		for k := 2; k < order; k++ {
			lpcPred += s[ix-1-k] * predCoef[k]
		}
		rLPC[ix] = s[ix] - lpcPred
	}
	clear(rLPC[:order])
}
