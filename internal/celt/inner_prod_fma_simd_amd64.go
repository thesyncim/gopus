//go:build amd64 && goexperiment.simd && !purego

package celt

import "simd/archsimd"

// celtInnerProd8FMA32 uses the archsimd 4-lane FMA accumulator when the CPU has
// FMA (archsimd MulAdd lowers to VFMADD, which has no non-FMA encoding); without
// it, it falls back to the scalar reference, which fuses through math.FMA. Both
// keep the same 4-lane order and reduction, so the result is bit-exact either way.
func celtInnerProd8FMA32(x, y []float32, n int) float32 {
	if n <= 0 {
		return 0
	}
	if archsimd.X86.FMA() {
		return innerProd8FMA32ArchSIMD(x, y, n)
	}
	return innerProd8FMA32Scalar(x, y, n)
}

// innerProd8FMA32Scalar reproduces the 4-lane fused accumulation and reduction
// order with scalar math.FMA, for amd64 CPUs without the FMA feature.
func innerProd8FMA32Scalar(x, y []float32, n int) float32 {
	x = x[:n]
	y = y[:n]
	var acc0, acc1, acc2, acc3 float32
	for len(x) >= 4 && len(y) >= 4 {
		acc0 = mdctFMA32(x[0], y[0], acc0)
		acc1 = mdctFMA32(x[1], y[1], acc1)
		acc2 = mdctFMA32(x[2], y[2], acc2)
		acc3 = mdctFMA32(x[3], y[3], acc3)
		x = x[4:]
		y = y[4:]
	}
	sum0 := round32(acc0 + acc2)
	sum1 := round32(acc1 + acc3)
	sum := round32(sum0 + sum1)
	for i := 0; i < len(x) && i < len(y); i++ {
		sum = mdctFMA32(x[i], y[i], sum)
	}
	return sum
}
