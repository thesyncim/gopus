//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"testing"
)

var simdAllocSink32 float32
var simdAllocSink8 [8]float32

func TestAMD64SIMDKernelsZeroAlloc(t *testing.T) {
	const n = 241
	x := make([]float32, n+7)
	y := make([]float32, n+7)
	for i := range x {
		x[i] = float32(i%17) * 0.03125
		y[i] = float32(i%13) * -0.0625
	}
	cx := make([]celtNorm, n)
	cy := make([]celtNorm, n)
	for i := range cx {
		cx[i] = celtNorm(x[i])
		cy[i] = celtNorm(y[i])
	}

	celtInnerProdSSEStyleImpl(cx, cy)
	if allocs := testing.AllocsPerRun(100, func() {
		simdAllocSink32 = celtInnerProdSSEStyleImpl(cx, cy)
	}); allocs != 0 {
		t.Fatalf("inner product allocated %v times", allocs)
	}

	pitchXcorrKernelAVX8(x[:n], y, &simdAllocSink8, n)
	if allocs := testing.AllocsPerRun(100, func() {
		pitchXcorrKernelAVX8(x[:n], y, &simdAllocSink8, n)
	}); allocs != 0 {
		t.Fatalf("pitch xcorr allocated %v times", allocs)
	}
}
