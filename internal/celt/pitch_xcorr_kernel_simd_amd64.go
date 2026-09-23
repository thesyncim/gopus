//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"simd/archsimd"
	"unsafe"
)

func xcorrKernelAVX8(x, y *float32, sum *[8]float32, length int) {
	if length <= 0 {
		*sum = [8]float32{}
		return
	}
	if !archsimd.X86.FMA() {
		xcorrKernelAVX8ScalarGo(x, y, sum, length)
		return
	}

	xp := unsafe.Pointer(x)
	yp := unsafe.Pointer(y)
	var acc [8]archsimd.Float32x8
	i := 0
	for ; i+8 <= length; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(xp))
		for corr := range 8 {
			yv := archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, uintptr(corr*4))))
			acc[corr] = xv.MulAdd(yv, acc[corr])
		}
		xp = unsafe.Add(xp, 32)
		yp = unsafe.Add(yp, 32)
	}

	var lanes [8][8]float32
	for corr := range 8 {
		acc[corr].StoreArray(&lanes[corr])
	}
	for lane := 0; i+lane < length; lane++ {
		xv := *(*float32)(unsafe.Add(xp, uintptr(lane*4)))
		for corr := range 8 {
			p := unsafe.Add(yp, uintptr((lane+corr)*4))
			lanes[corr][lane] = mdctFMA32(xv, *(*float32)(p), lanes[corr][lane])
		}
	}
	for corr := range 8 {
		sum[corr] = reduceAVX2PitchSum(lanes[corr])
	}
}

func xcorrKernelAVX8ScalarGo(x, y *float32, sum *[8]float32, length int) {
	xs := unsafe.Slice(x, length)
	ys := unsafe.Slice(y, length+7)
	var lanes [8][8]float32
	for i := range xs {
		xv := xs[i]
		for corr := range 8 {
			lane := i & 7
			lanes[corr][lane] = mdctFMA32(xv, ys[i+corr], lanes[corr][lane])
		}
	}
	for corr := range 8 {
		sum[corr] = reduceAVX2PitchSum(lanes[corr])
	}
}

func pitchXcorrKernelAVX8(x, y []float32, sum *[8]float32, length int) {
	xcorrKernelAVX8(&x[0], &y[0], sum, length)
}
