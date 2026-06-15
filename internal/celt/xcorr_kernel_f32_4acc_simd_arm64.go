//go:build arm64 && goexperiment.simd && !purego

package celt

import "simd/archsimd"

// pitchXcorrUsesNeonFMA selects the four-phase fused xcorr kernel on the arm64
// experiment build, exactly as the asm build does.
const pitchXcorrUsesNeonFMA = true

// xcorrKernel4Float32Neon4Acc accumulates the four-lag float cross-correlation
// with four phase-parallel archsimd MulAdd (FMLA) chains — one Float32x4 per
// sample in each 4-sample block, lane k holding lag k. Phase p loads y[p:p+4]
// and fuses x[p] into it, matching the scalar reference's per-phase order; the
// phases combine as (acc0+acc1)+(acc2+acc3) and a scalar-order tail finishes the
// remainder. Every op is a fused FMLA or a bare FADD, so the result is bit-exact
// with xcorrKernel4Float32FourAccRef (TestXcorrKernel4Float32Neon4AccBitExact).
func xcorrKernel4Float32Neon4Acc(x, y []float32, sum *[4]float32, length int) {
	if length <= 0 {
		return
	}
	x = x[:length]
	y = y[:length+3]
	acc0 := archsimd.LoadFloat32x4(sum[:])
	zero := archsimd.BroadcastFloat32x4(0)
	acc1, acc2, acc3 := zero, zero, zero
	blocked := length >= 4
	for len(x) >= 4 && len(y) >= 7 {
		acc0 = archsimd.BroadcastFloat32x4(x[0]).MulAdd(archsimd.LoadFloat32x4(y[0:]), acc0)
		acc1 = archsimd.BroadcastFloat32x4(x[1]).MulAdd(archsimd.LoadFloat32x4(y[1:]), acc1)
		acc2 = archsimd.BroadcastFloat32x4(x[2]).MulAdd(archsimd.LoadFloat32x4(y[2:]), acc2)
		acc3 = archsimd.BroadcastFloat32x4(x[3]).MulAdd(archsimd.LoadFloat32x4(y[3:]), acc3)
		x = x[4:]
		y = y[4:]
	}
	combined := acc0
	if blocked {
		combined = acc0.Add(acc1).Add(acc2.Add(acc3))
	}
	for len(x) >= 1 && len(y) >= 4 {
		combined = archsimd.BroadcastFloat32x4(x[0]).MulAdd(archsimd.LoadFloat32x4(y[0:]), combined)
		x = x[1:]
		y = y[1:]
	}
	combined.Store(sum[:])
}
