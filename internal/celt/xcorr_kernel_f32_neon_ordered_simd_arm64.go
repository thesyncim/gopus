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
	x = x[:length:length]
	y = y[: length+3 : length+3]
	acc := archsimd.LoadFloat32x4Array(sum)
	i := 0
	for ; i+2 <= length; i += 2 {
		acc = archsimd.LoadFloat32x4Array((*[4]float32)(y[i : i+4])).MulAdd(archsimd.BroadcastFloat32x4(x[i]), acc)
		acc = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+1 : i+5])).MulAdd(archsimd.BroadcastFloat32x4(x[i+1]), acc)
	}
	for ; i < length; i++ {
		acc = archsimd.LoadFloat32x4Array((*[4]float32)(y[i : i+4])).MulAdd(archsimd.BroadcastFloat32x4(x[i]), acc)
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
	x = x[:length:length]
	y = y[: length+15 : length+15]
	var acc0, acc1, acc2, acc3 archsimd.Float32x4
	i := 0
	for ; i+2 <= length; i += 2 {
		bx0 := archsimd.BroadcastFloat32x4(x[i])
		bx1 := archsimd.BroadcastFloat32x4(x[i+1])
		acc0 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i : i+4])).MulAdd(bx0, acc0)
		acc1 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+4 : i+8])).MulAdd(bx0, acc1)
		acc2 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+8 : i+12])).MulAdd(bx0, acc2)
		acc3 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+12 : i+16])).MulAdd(bx0, acc3)
		acc0 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+1 : i+5])).MulAdd(bx1, acc0)
		acc1 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+5 : i+9])).MulAdd(bx1, acc1)
		acc2 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+9 : i+13])).MulAdd(bx1, acc2)
		acc3 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+13 : i+17])).MulAdd(bx1, acc3)
	}
	for ; i < length; i++ {
		bx := archsimd.BroadcastFloat32x4(x[i])
		acc0 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i : i+4])).MulAdd(bx, acc0)
		acc1 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+4 : i+8])).MulAdd(bx, acc1)
		acc2 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+8 : i+12])).MulAdd(bx, acc2)
		acc3 = archsimd.LoadFloat32x4Array((*[4]float32)(y[i+12 : i+16])).MulAdd(bx, acc3)
	}
	acc0.StoreArray((*[4]float32)(dst[0:4]))
	acc1.StoreArray((*[4]float32)(dst[4:8]))
	acc2.StoreArray((*[4]float32)(dst[8:12]))
	acc3.StoreArray((*[4]float32)(dst[12:16]))
}
