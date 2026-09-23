//go:build amd64 && goexperiment.simd && !nosimd

package celt

import "testing"

func TestCombFilterConstSSEOrderZeroAllocs(t *testing.T) {
	const n = 480
	dst := make([]float32, n)
	delay := make([]float32, n)
	for i := range delay {
		delay[i] = float32((i*37)%191-95) / 64
	}
	run := func() {
		combFilterConstFloat32(dst, delay, 0.25, 0.125, 0.0625, 0, 0, 0, 0)
	}
	run()
	if got := testing.AllocsPerRun(100, run); got != 0 {
		t.Fatalf("comb filter allocated: %g allocs/run", got)
	}
}
