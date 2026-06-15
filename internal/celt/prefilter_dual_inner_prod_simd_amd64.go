//go:build amd64 && goexperiment.simd && !purego

package celt

import "simd/archsimd"

// prefilterDualInnerProdAsm uses the archsimd 4-lane FMA accumulators when the CPU
// has FMA (MulAdd lowers to VFMADD); without it, it falls back to the scalar
// reference, which fuses through math.FMA. Both keep the same 4-lane order and
// reduction, so the result is bit-exact either way.
func prefilterDualInnerProdAsm(x, y1, y2 []float32, length int) (float32, float32) {
	if length <= 0 {
		return 0, 0
	}
	if archsimd.X86.FMA() {
		return prefilterDualInnerProdArchSIMD(x, y1, y2, length)
	}
	return prefilterDualInnerProdScalar(x, y1, y2, length)
}

// prefilterDualInnerProdScalar reproduces the 4-lane fused accumulation and
// reduction order with scalar math.FMA, for amd64 CPUs without the FMA feature.
func prefilterDualInnerProdScalar(x, y1, y2 []float32, length int) (float32, float32) {
	x = x[:length]
	y1 = y1[:length]
	y2 = y2[:length]
	var a10, a11, a12, a13 float32
	var a20, a21, a22, a23 float32
	for len(x) >= 4 && len(y1) >= 4 && len(y2) >= 4 {
		a10 = mdctFMA32(x[0], y1[0], a10)
		a20 = mdctFMA32(x[0], y2[0], a20)
		a11 = mdctFMA32(x[1], y1[1], a11)
		a21 = mdctFMA32(x[1], y2[1], a21)
		a12 = mdctFMA32(x[2], y1[2], a12)
		a22 = mdctFMA32(x[2], y2[2], a22)
		a13 = mdctFMA32(x[3], y1[3], a13)
		a23 = mdctFMA32(x[3], y2[3], a23)
		x = x[4:]
		y1 = y1[4:]
		y2 = y2[4:]
	}
	sum1 := round32(round32(a10+a12) + round32(a11+a13))
	sum2 := round32(round32(a20+a22) + round32(a21+a23))
	for i := 0; i < len(x) && i < len(y1) && i < len(y2); i++ {
		sum1 = mdctFMA32(x[i], y1[i], sum1)
		sum2 = mdctFMA32(x[i], y2[i], sum2)
	}
	return sum1, sum2
}
