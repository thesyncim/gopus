//go:build arm64 && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// celtAbsSumUsesNeon selects the archsimd float abs-sum on the arm64 experiment
// build, exactly as the asm build does.
const celtAbsSumUsesNeon = true

// l1AbsSumNeon returns the sum of absolute values of the first n elements with a
// 4-lane archsimd accumulator (Abs + Add), loading through raw pointers
// (loadF32x4) to drop the per-load slice bounds check. Lane k sums |tmp[k]|,
// |tmp[k+4]|, … and the reduction is (a0+a1)+(a2+a3)+tail — the exact order of
// l1AbsSumNeonReference, so it is bit-exact with the NEON kernel it replaces
// (TestL1AbsSumNeonBitExact). That order diverges from the scalar L1 sum by a few
// ULP — the arm64 quality-gated regime — so amd64 and purego keep the scalar sum.
func l1AbsSumNeon(tmp []float32, n int) float32 {
	acc := archsimd.BroadcastFloat32x4(0)
	tp := unsafe.Pointer(unsafe.SliceData(tmp))
	i := 0
	for ; i+8 <= n; i += 8 {
		acc = acc.Add(loadF32x4(tp).Abs())
		acc = acc.Add(loadF32x4(unsafe.Add(tp, 16)).Abs())
		tp = unsafe.Add(tp, 32)
	}
	for ; i+4 <= n; i += 4 {
		acc = acc.Add(loadF32x4(tp).Abs())
		tp = unsafe.Add(tp, 16)
	}
	var tail float32
	for ; i < n; i++ {
		v := *(*float32)(tp)
		if v < 0 {
			v = -v
		}
		tail += v
		tp = unsafe.Add(tp, 4)
	}
	return ((acc.GetElem(0) + acc.GetElem(1)) + (acc.GetElem(2) + acc.GetElem(3))) + tail
}
