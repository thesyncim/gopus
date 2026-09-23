//go:build amd64 && goexperiment.simd && !nosimd

package silk

import (
	"simd/archsimd"
	"unsafe"

	"github.com/thesyncim/gopus/internal/opusmath"
)

var silkUsePitchXcorrAVX2FMA = archsimd.X86.AVX2() && archsimd.X86.FMA()

func xcorrKernelAVX8(x, y *float32, sum *[8]float32, length int) {
	if length <= 0 {
		*sum = [8]float32{}
		return
	}
	if !silkUsePitchXcorrAVX2FMA {
		xcorrKernelAVX8ScalarGo(x, y, sum, length)
		return
	}
	if length < 16 {
		// Setup and lane extraction cost more than the short scalar loop for the
		// tiny SILK pitch-search shapes.
		xcorrKernelAVX8ScalarGo(x, y, sum, length)
		return
	}

	// Run four correlations at a time. Keeping eight vector accumulators live
	// alongside the x/y vectors spills them in the sample loop on amd64.
	xcorrKernelAVX4(x, y, (*[4]float32)(unsafe.Pointer(&sum[0])), length)
	xcorrKernelAVX4(x, (*float32)(unsafe.Add(unsafe.Pointer(y), 16)), (*[4]float32)(unsafe.Pointer(&sum[4])), length)
}

func xcorrKernelAVX4(x, y *float32, sum *[4]float32, length int) {
	xp := unsafe.Pointer(x)
	yp := unsafe.Pointer(y)
	var acc0, acc1, acc2, acc3 archsimd.Float32x8
	i := 0
	for ; i+8 <= length; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(xp))
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yp)), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 4))), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 8))), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 12))), acc3)
		xp = unsafe.Add(xp, 32)
		yp = unsafe.Add(yp, 32)
	}
	if i < length {
		remaining := length - i
		xTail := loadXcorrTail8(xp, remaining)
		acc0 = xTail.MulAdd(loadXcorrTail8(yp, remaining), acc0)
		acc1 = xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 4), remaining), acc1)
		acc2 = xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 8), remaining), acc2)
		acc3 = xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 12), remaining), acc3)
	}
	sum[0] = reduceXcorrAVX8(acc0)
	sum[1] = reduceXcorrAVX8(acc1)
	sum[2] = reduceXcorrAVX8(acc2)
	sum[3] = reduceXcorrAVX8(acc3)
}

func loadXcorrTail8(p unsafe.Pointer, remaining int) archsimd.Float32x8 {
	// Read only valid tail lanes before loading the stack vector, so a tail at a
	// page boundary never turns into a full-width memory read.
	var lanes [8]float32
	for lane := range 8 {
		if lane < remaining {
			lanes[lane] = *(*float32)(unsafe.Add(p, uintptr(lane*4)))
		}
	}
	return archsimd.LoadFloat32x8Array(&lanes)
}

func reduceXcorrAVX8(v archsimd.Float32x8) float32 {
	v = v.Add(v.ConcatPermute128Scalars(1, 0, v))
	v = v.ConcatAddPairsGrouped(v)
	v = v.ConcatAddPairsGrouped(v)
	return v.GetLo().GetElem(0)
}

func xcorrKernelAVX8ScalarGo(x, y *float32, sum *[8]float32, length int) {
	xs := unsafe.Slice(x, length)
	ys := unsafe.Slice(y, length+7)
	var lanes [8][8]float32
	for i := range xs {
		xv := xs[i]
		for corr := range 8 {
			lane := i & 7
			yv := ys[i+corr]
			if i < 8 {
				// The AVX2 lane starts at +0. Its first fused multiply-add is
				// the product rounded to float32. Keep the FMA for zero or
				// non-finite inputs to preserve its signed-zero and NaN rules.
				product := xv * yv
				if xv != 0 && yv != 0 && product == product {
					lanes[corr][lane] = product
				} else {
					lanes[corr][lane] = opusmath.FMA32(xv, yv, 0)
				}
			} else {
				lanes[corr][lane] = opusmath.FMA32(xv, yv, lanes[corr][lane])
			}
		}
	}
	for corr := range 8 {
		v := lanes[corr]
		s04 := v[0] + v[4]
		s15 := v[1] + v[5]
		s26 := v[2] + v[6]
		s37 := v[3] + v[7]
		sum[corr] = (s04 + s15) + (s26 + s37)
	}
}
