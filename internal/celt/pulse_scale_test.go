package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestScalePulsesMatchesScalar checks the build-selected pulse scaling
// against the scalar loop, bit for bit, on random pulse vectors.
func TestScalePulsesMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x9e75))
	gains := []float32{1, 0.5, math.Float32frombits(0x3dbd1c75), -0.75, 1e-20, 3e10}
	for trial := range 2000 {
		n := rng.Intn(200)
		pulses := make([]int32, n)
		for i := range pulses {
			if rng.Intn(3) != 0 {
				pulses[i] = int32(rng.Intn(65) - 32)
			}
		}
		g := gains[trial%len(gains)] * float32(1+rng.Float64())
		got := make([]celtNorm, n)
		want := make([]celtNorm, n)
		scalePulsesInto(got, pulses, g)
		scalePulsesIntoScalar(want, pulses, g)
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("trial %d n=%d: out[%d]=%08x want %08x", trial, n, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
			}
		}
	}
	out := make([]celtNorm, 96)
	pulses := make([]int32, 96)
	if allocs := testing.AllocsPerRun(100, func() {
		scalePulsesInto(out, pulses, 0.5)
	}); allocs != 0 {
		t.Fatalf("pulse scaling allocated %v times", allocs)
	}
}
