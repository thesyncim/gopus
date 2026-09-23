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

	xp := unsafe.Pointer(x)
	yp := unsafe.Pointer(y)
	// Four correlations per pass keep the live SIMD accumulators in registers.
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
		mask := xcorrTailMask8(remaining)
		acc0 = mergeXcorrTail8(xTail.MulAdd(loadXcorrTail8(yp, remaining), acc0), acc0, mask)
		acc1 = mergeXcorrTail8(xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 4), remaining), acc1), acc1, mask)
		acc2 = mergeXcorrTail8(xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 8), remaining), acc2), acc2, mask)
		acc3 = mergeXcorrTail8(xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 12), remaining), acc3), acc3, mask)
	}
	sum[0] = reduceXcorrAVX8(acc0)
	sum[1] = reduceXcorrAVX8(acc1)
	sum[2] = reduceXcorrAVX8(acc2)
	sum[3] = reduceXcorrAVX8(acc3)

	var acc4, acc5, acc6, acc7 archsimd.Float32x8
	xp, yp = unsafe.Pointer(x), unsafe.Pointer(y)
	i = 0
	for ; i+8 <= length; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(xp))
		acc4 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 16))), acc4)
		acc5 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 20))), acc5)
		acc6 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 24))), acc6)
		acc7 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 28))), acc7)
		xp = unsafe.Add(xp, 32)
		yp = unsafe.Add(yp, 32)
	}
	if i < length {
		remaining := length - i
		xTail := loadXcorrTail8(xp, remaining)
		mask := xcorrTailMask8(remaining)
		acc4 = mergeXcorrTail8(xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 16), remaining), acc4), acc4, mask)
		acc5 = mergeXcorrTail8(xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 20), remaining), acc5), acc5, mask)
		acc6 = mergeXcorrTail8(xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 24), remaining), acc6), acc6, mask)
		acc7 = mergeXcorrTail8(xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 28), remaining), acc7), acc7, mask)
	}
	sum[4] = reduceXcorrAVX8(acc4)
	sum[5] = reduceXcorrAVX8(acc5)
	sum[6] = reduceXcorrAVX8(acc6)
	sum[7] = reduceXcorrAVX8(acc7)
}

func xcorrTailMask8(remaining int) archsimd.Int32x8 {
	var lanes [8]int32
	for lane := range remaining {
		lanes[lane] = -1
	}
	return archsimd.LoadInt32x8Array(&lanes)
}

func mergeXcorrTail8(updated, original archsimd.Float32x8, mask archsimd.Int32x8) archsimd.Float32x8 {
	updatedBits := updated.AsInt32x8()
	originalBits := original.AsInt32x8()
	return updatedBits.And(mask).Or(originalBits.AndNot(mask)).AsFloat32x8()
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
			lanes[corr][lane] = opusmath.FMA32(xv, ys[i+corr], lanes[corr][lane])
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
