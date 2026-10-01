//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"math"
	"math/rand"
	"testing"
)

func TestLPCAnalysisFilterF32MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1fc0))
	for trial := range 2000 {
		order := [...]int{6, 8, 10, 12, 16, 7}[trial%6]
		length := order + rng.Intn(400)
		s := make([]float32, length)
		scale := float32(math.Pow(10, float64(rng.Intn(12)-4)))
		for i := range s {
			s[i] = (rng.Float32()*2 - 1) * scale
			if rng.Intn(50) == 0 {
				s[i] = [...]float32{0, float32(math.Copysign(0, -1)), float32(math.Inf(1)), float32(math.NaN()), 1e-40}[rng.Intn(5)]
			}
		}
		coef := make([]float32, order)
		for i := range coef {
			coef[i] = rng.Float32()*4 - 2
		}
		got := make([]float32, length)
		want := make([]float32, length)
		for i := range got {
			got[i] = float32(i)
			want[i] = float32(i)
		}
		lpcAnalysisFilterF32(got, coef, s, length, order)
		lpcAnalysisFilterF32Scalar(want, coef, s, length, order)
		for i := range got {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("trial %d order %d length %d: out[%d] = %v (%#x), want %v (%#x)", trial, order, length, i,
					got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
			}
		}
	}
}

func TestLPCAnalysisFilterF32ZeroAlloc(t *testing.T) {
	s := make([]float32, 320)
	for i := range s {
		s[i] = float32(i%17) - 8
	}
	coef := make([]float32, maxLPCOrder)
	for i := range coef {
		coef[i] = 0.01 * float32(i+1)
	}
	out := make([]float32, len(s))
	if allocs := testing.AllocsPerRun(100, func() {
		lpcAnalysisFilterF32(out, coef, s, len(s), maxLPCOrder)
	}); allocs != 0 {
		t.Fatalf("LPC analysis filter allocated %v times", allocs)
	}
}
