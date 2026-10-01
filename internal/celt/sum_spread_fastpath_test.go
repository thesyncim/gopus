package celt

import (
	"math"
	"math/rand"
	"testing"
)

func spreadCountThresholdsLegacy(x []float64, n int, nf float64) (t0, t1, t2 int) {
	for j := range n {
		v := x[j]
		x2N := v * v * nf
		if x2N < 0.25 {
			t0++
		}
		if x2N < 0.0625 {
			t1++
		}
		if x2N < 0.015625 {
			t2++
		}
	}
	return
}

func makeSumSpreadFastpathInput(n int) []float64 {
	out := make([]float64, n)
	x := uint32(0x12345678)
	for i := range out {
		x = 1664525*x + 1013904223
		v := float64(int32(x>>8)%4096) / 257.0
		if i%7 == 0 {
			v *= -1
		}
		out[i] = v
	}
	return out
}

func TestSpreadCountThresholdsMatchesLegacy(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 15, 16, 31, 32, 63, 64} {
		x := makeSumSpreadFastpathInput(n)
		got0, got1, got2 := spreadCountThresholds(float64sToNorms(x), n, float32(0.375))
		want0, want1, want2 := spreadCountThresholdsLegacy(x, n, 0.375)
		if got0 != want0 || got1 != want1 || got2 != want2 {
			t.Fatalf("n=%d mismatch: got=(%d,%d,%d) want=(%d,%d,%d)", n, got0, got1, got2, want0, want1, want2)
		}
	}
}

// TestSpreadCountThresholdsRandomMatchesBranchyLoop checks the build-selected
// counter against the branchy spreading_decision() loop on random bands,
// including threshold-exact, NaN and infinite coefficients.
func TestSpreadCountThresholdsRandomMatchesBranchyLoop(t *testing.T) {
	rng := rand.New(rand.NewSource(0x5d7e))
	specials := []float32{0, float32(math.Copysign(0, -1)), 0.5, -0.25, 0.125, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))}
	for trial := range 2000 {
		n := 1 + rng.Intn(180)
		x := make([]celtNorm, n)
		for i := range x {
			if rng.Intn(8) == 0 {
				x[i] = celtNorm(specials[rng.Intn(len(specials))])
			} else {
				x[i] = celtNorm(float32(rng.NormFloat64()) / float32(math.Sqrt(float64(n))))
			}
		}
		nf := float32(n)
		if trial%5 == 0 {
			nf = 1
		}
		var w0, w1, w2 int
		for _, v := range x {
			x2N := float32(v) * float32(v) * nf
			if x2N < 0.25 {
				w0++
			}
			if x2N < 0.0625 {
				w1++
			}
			if x2N < 0.015625 {
				w2++
			}
		}
		g0, g1, g2 := spreadCountThresholds(x, n, nf)
		if g0 != w0 || g1 != w1 || g2 != w2 {
			t.Fatalf("trial %d n=%d: got (%d,%d,%d) want (%d,%d,%d)", trial, n, g0, g1, g2, w0, w1, w2)
		}
	}
}
