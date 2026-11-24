//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// celtAbsSumUsesNeon selects the archsimd float abs-sum on the arm64 experiment
// build.
const celtAbsSumUsesNeon = true

// l1AbsSumNeon returns the sum of absolute values of the first n elements with a
// 4-lane archsimd accumulator (Abs + Add). Lane k sums |tmp[k]|, |tmp[k+4]|, …
// and the reduction is (a0+a1)+(a2+a3)+tail — matching libopus's NEON reduction
// order (TestL1AbsSumNeonBitExact).
func l1AbsSumNeon(tmp []float32, n int) float32 {
	if n <= 0 {
		return 0
	}
	tmp = tmp[:n]
	acc := archsimd.BroadcastFloat32x4(0)
	i := 0
	for ; i+8 <= n; i += 8 {
		acc = acc.Add(archsimd.LoadFloat32x4Array((*[4]float32)(tmp[i:])).Abs())
		acc = acc.Add(archsimd.LoadFloat32x4Array((*[4]float32)(tmp[i+4:])).Abs())
	}
	for ; i+4 <= n; i += 4 {
		acc = acc.Add(archsimd.LoadFloat32x4Array((*[4]float32)(tmp[i:])).Abs())
	}
	var tail float32
	for ; i < n; i++ {
		v := tmp[i]
		if v < 0 {
			v = -v
		}
		tail += v
	}
	return ((acc.GetElem(0) + acc.GetElem(1)) + (acc.GetElem(2) + acc.GetElem(3))) + tail
}
