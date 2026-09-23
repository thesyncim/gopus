//go:build amd64 && goexperiment.simd && !nosimd

package silk

import "testing"

var simdAllocSink32 float64
var simdAllocSink8 [8]float32

func TestAMD64SIMDKernelsZeroAlloc(t *testing.T) {
	const n = 241
	x := make([]float32, n+7)
	y := make([]float32, n+7)
	for i := range x {
		x[i] = float32(i%17) * 0.03125
		y[i] = float32(i%13) * -0.0625
	}

	innerProductFLPImpl(x, y, n)
	if allocs := testing.AllocsPerRun(100, func() {
		simdAllocSink32 = float64(innerProductFLPImpl(x, y, n))
	}); allocs != 0 {
		t.Fatalf("inner product allocated %v times", allocs)
	}

	xcorrKernelAVX8(&x[0], &y[0], &simdAllocSink8, n)
	if allocs := testing.AllocsPerRun(100, func() {
		xcorrKernelAVX8(&x[0], &y[0], &simdAllocSink8, n)
	}); allocs != 0 {
		t.Fatalf("pitch xcorr allocated %v times", allocs)
	}
}
