//go:build darwin && arm64 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package celt

import "simd/archsimd"

const pitchAutocorrRoundFourTermTail = true

// pitchAutocorrTail4 follows the ordinary Darwin ARM64 C _celt_autocorr
// caller in celt/celt_lpc.c: four rounded vector products, then ordered scalar
// additions. Its one-to-three-term residuals use the existing fused path.
func pitchAutocorrTail4(x, y []float32) float32 {
	products := archsimd.LoadFloat32x4(x).Mul(archsimd.LoadFloat32x4(y))
	sum := float32(0) + products.GetElem(0)
	sum += products.GetElem(1)
	sum += products.GetElem(2)
	sum += products.GetElem(3)
	return sum
}
