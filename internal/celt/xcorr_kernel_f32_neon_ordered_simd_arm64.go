//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// pitchXcorrUsesNeonFMA selects libopus' fused NEON pitch xcorr on arm64.
const pitchXcorrUsesNeonFMA = true

// xcorrKernel4Float32NeonOrdered follows celt/arm/celt_neon_intr.c's
// xcorr_kernel_neon_float: one Float32x4 accumulator holds four adjacent lags,
// and each input sample updates it in ascending order. The y receiver preserves
// the C FMLA operand order for distinct NaN payloads.
func xcorrKernel4Float32NeonOrdered(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	_ = x[length-1]
	_ = y[length+2]
	acc := archsimd.LoadFloat32x4Array(sum)
	for i := 0; i < length; i++ {
		acc = archsimd.LoadFloat32x4Array((*[4]float32)(y[i:])).MulAdd(archsimd.BroadcastFloat32x4(x[i]), acc)
	}
	acc.StoreArray(sum)
}

// xcorrKernel16Float32NeonOrdered runs four independent 4-lag FMLA chains in
// parallel across x[0:length], saturating arm64's multi-issue FMA pipelines and
// broadcasting each x[i] once per 16 lags. Each lag executes the exact same
// ascending-i FMLA sequence as xcorrKernel4Float32NeonOrdered (0-ULP bit-exact).
func xcorrKernel16Float32NeonOrdered(x, y []float32, dst *[16]float32, length int) {
	if length <= 0 {
		*dst = [16]float32{}
		return
	}
	_ = x[length-1]
	_ = y[length+14]
	var acc0, acc1, acc2, acc3 archsimd.Float32x4
	for i := 0; i < length; i++ {
		bx := archsimd.BroadcastFloat32x4(x[i])
		yi := y[i : i+16 : i+16]
		acc0 = archsimd.LoadFloat32x4Array((*[4]float32)(yi[0:4])).MulAdd(bx, acc0)
		acc1 = archsimd.LoadFloat32x4Array((*[4]float32)(yi[4:8])).MulAdd(bx, acc1)
		acc2 = archsimd.LoadFloat32x4Array((*[4]float32)(yi[8:12])).MulAdd(bx, acc2)
		acc3 = archsimd.LoadFloat32x4Array((*[4]float32)(yi[12:16])).MulAdd(bx, acc3)
	}
	acc0.StoreArray((*[4]float32)(dst[0:4]))
	acc1.StoreArray((*[4]float32)(dst[4:8]))
	acc2.StoreArray((*[4]float32)(dst[8:12]))
	acc3.StoreArray((*[4]float32)(dst[12:16]))
}
