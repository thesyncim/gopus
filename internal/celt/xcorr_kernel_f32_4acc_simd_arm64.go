//go:build arm64 && goexperiment.simd && !purego

package celt

import (
	"simd/archsimd"
	"unsafe"
)

// pitchXcorrUsesNeonFMA selects the four-phase fused xcorr kernel on the arm64
// experiment build, exactly as the asm build does.
const pitchXcorrUsesNeonFMA = true

// xcorrKernel4Float32Neon4Acc accumulates the four-lag float cross-correlation
// with four phase-parallel archsimd MulAdd (FMLA) chains — one Float32x4 per
// sample in each 4-sample block, lane k holding lag k. Phase p broadcasts x[p]
// and fuses it into y[p:p+4]; the phases combine as (acc0+acc1)+(acc2+acc3) with a
// scalar-order tail, matching xcorrKernel4Float32FourAccRef bit-for-bit
// (TestXcorrKernel4Float32Neon4AccBitExact). Loads go through raw pointers
// (loadF32x4) to drop the per-load slice bounds check.
func xcorrKernel4Float32Neon4Acc(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	_ = x[:length]
	_ = y[:length+3]
	acc0 := loadF32x4(unsafe.Pointer(sum))
	zero := archsimd.BroadcastFloat32x4(0)
	acc1, acc2, acc3 := zero, zero, zero
	xp := unsafe.Pointer(unsafe.SliceData(x))
	yp := unsafe.Pointer(unsafe.SliceData(y))
	blocked := length >= 4
	i := 0
	for ; i+4 <= length; i += 4 {
		acc0 = archsimd.BroadcastFloat32x4(*(*float32)(xp)).MulAdd(loadF32x4(yp), acc0)
		acc1 = archsimd.BroadcastFloat32x4(*(*float32)(unsafe.Add(xp, 4))).MulAdd(loadF32x4(unsafe.Add(yp, 4)), acc1)
		acc2 = archsimd.BroadcastFloat32x4(*(*float32)(unsafe.Add(xp, 8))).MulAdd(loadF32x4(unsafe.Add(yp, 8)), acc2)
		acc3 = archsimd.BroadcastFloat32x4(*(*float32)(unsafe.Add(xp, 12))).MulAdd(loadF32x4(unsafe.Add(yp, 12)), acc3)
		xp = unsafe.Add(xp, 16)
		yp = unsafe.Add(yp, 16)
	}
	combined := acc0
	if blocked {
		combined = acc0.Add(acc1).Add(acc2.Add(acc3))
	}
	for ; i < length; i++ {
		combined = archsimd.BroadcastFloat32x4(*(*float32)(xp)).MulAdd(loadF32x4(yp), combined)
		xp = unsafe.Add(xp, 4)
		yp = unsafe.Add(yp, 4)
	}
	storeF32x4(unsafe.Pointer(sum), combined)
}
