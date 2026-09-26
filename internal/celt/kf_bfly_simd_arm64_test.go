//go:build arm64 && goexperiment.simd && !nosimd

package celt

import "testing"

func TestKfBflySIMDSteadyStateAllocs(t *testing.T) {
	const m, n, fstride = 8, 4, 8
	w := make([]kissCpx, (5*m-1)*fstride+1)
	for i := range w {
		w[i] = kissCpx{r: float32(i%19) * 0.03125, i: float32(i%23) * -0.015625}
	}

	checkNoAllocs := func(name string, fn func()) {
		t.Helper()
		fn()
		if allocs := testing.AllocsPerRun(100, fn); allocs != 0 {
			t.Errorf("%s steady-state allocations = %g, want 0", name, allocs)
		}
	}

	// Keep the work buffers outside the measured closures so the measurement
	// covers only the butterfly kernels.
	foutM1 := kernelPortBenchComplex(128 * 4)
	fout3 := kernelPortBenchComplex(n * 3 * m)
	fout4 := kernelPortBenchComplex(n * 4 * m)
	fout5 := kernelPortBenchComplex(n * 5 * m)
	checkNoAllocs("radix4-m1", func() { kfBfly4M1Core(foutM1, 128) })
	checkNoAllocs("radix3-inner", func() { kfBfly3Inner(fout3, w, m, n, 3*m, fstride) })
	checkNoAllocs("radix4-inner", func() { kfBfly4Inner(fout4, w, m, n, 4*m, fstride) })
	checkNoAllocs("radix5-inner", func() { kfBfly5Inner(fout5, w, m, n, 5*m, fstride) })
}
