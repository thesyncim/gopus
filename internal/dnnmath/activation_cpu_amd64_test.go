//go:build amd64 && goexperiment.simd && !nosimd && !purego

package dnnmath

import (
	"math"
	"simd/archsimd"
	"testing"
)

func TestDNNX86CPUFallback(t *testing.T) {
	if X86VectorKernels && (!archsimd.X86.AVX2() || !archsimd.X86.FMA()) {
		t.Fatal("DNN vector dispatch requires AVX2 and FMA")
	}
	saved := X86VectorKernels
	X86VectorKernels = false
	t.Cleanup(func() { X86VectorKernels = saved })
	input := [17]float32{-10, -1, -0.5, 0, 0.5, 1, 10, 0.125, -0.125, 2, -2, 3, -3, 4, -4, 0.25, -0.25}
	for _, tc := range []struct {
		name             string
		selected, scalar func([]float32, []float32, int)
	}{
		{"sigmoid", SigmoidVectorApprox, SigmoidVectorScalarApprox},
		{"tanh", TanhVectorApprox, TanhVectorScalarApprox},
		{"exp", ExpVectorApprox, ExpVectorScalarApprox},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got, want [17]float32
			for n := range len(input) + 1 {
				tc.selected(got[:], input[:], n)
				tc.scalar(want[:], input[:], n)
				for i := range n {
					if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
						t.Fatalf("length %d lane %d: got %08x want %08x", n, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
					}
				}
			}
			if allocs := testing.AllocsPerRun(100, func() { tc.selected(got[:], input[:], len(input)) }); allocs != 0 {
				t.Fatalf("fallback allocations = %g", allocs)
			}
		})
	}
	for _, x := range input {
		if got, want := tanhApproxX86(x), TanhScalarApprox(x); math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("scalar tanh(%g): got %08x want %08x", x, math.Float32bits(got), math.Float32bits(want))
		}
	}
}
