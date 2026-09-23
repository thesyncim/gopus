//go:build amd64 && !goexperiment.simd && !nosimd

package celt

import (
	"math"
	"unsafe"
)

// xcorrKernelAVX8 preserves the eight-lane fused accumulation order used by
// libopus' x86 pitch search.
func xcorrKernelAVX8(x, y *float32, sum *[8]float32, length int) {
	xs := unsafe.Slice(x, length)
	ys := unsafe.Slice(y, length+7)
	var lanes [8][8]float32
	for i := range xs {
		xv := xs[i]
		for corr := range 8 {
			lane := i & 7
			lanes[corr][lane] = float32(math.FMA(float64(xv), float64(ys[i+corr]), float64(lanes[corr][lane])))
		}
	}
	for corr := range 8 {
		v := lanes[corr]
		sum[corr] = reduceAVX2PitchSum(v)
	}
}

func pitchXcorrKernelAVX8(x, y []float32, sum *[8]float32, length int) {
	xcorrKernelAVX8(&x[0], &y[0], sum, length)
}
