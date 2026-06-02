//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

func scalarL1Abs(tmp []float32, n int) float32 {
	var s float32
	for i := 0; i < n && i < len(tmp); i++ {
		v := tmp[i]
		if v < 0 {
			v = -v
		}
		s += v
	}
	return s
}

// TestL1AbsSumNeonCloseToScalar checks the NEON L1 abs-sum against the scalar
// reduction. The 4-lane order diverges by a few ULP relative; the metric only
// drives transient/TF decisions, well within the arm64 quality-gated regime.
func TestL1AbsSumNeonCloseToScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 15, 16, 31, 64, 120, 240, 481} {
		for trial := 0; trial < 100; trial++ {
			tmp := make([]float32, n)
			for i := range tmp {
				tmp[i] = float32(rng.NormFloat64()) * float32(rng.Intn(8))
			}
			got := l1AbsSumNeon(tmp, n)
			want := scalarL1Abs(tmp, n)
			diff := math.Abs(float64(got) - float64(want))
			scale := math.Max(1, math.Abs(float64(want)))
			if diff/scale > 1e-4 {
				t.Fatalf("n=%d trial=%d: neon=%v scalar=%v reldiff=%g", n, trial, got, want, diff/scale)
			}
		}
	}
}

// TestL1AbsSumNeonRespectsLen ensures n is clamped to len(tmp) by the caller
// contract (l1MetricNorm passes min(N,len)).
func TestL1AbsSumNeonRespectsLen(t *testing.T) {
	tmp := []float32{1, -2, 3, -4, 5}
	got := l1AbsSumNeon(tmp, len(tmp))
	want := scalarL1Abs(tmp, len(tmp))
	if math.Abs(float64(got-want)) > 1e-5 {
		t.Fatalf("got %v want %v", got, want)
	}
}

func BenchmarkL1AbsSumNeon(b *testing.B) {
	const n = 480
	rng := rand.New(rand.NewSource(7))
	tmp := make([]float32, n)
	for i := range tmp {
		tmp[i] = float32(rng.NormFloat64())
	}
	var sink float32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += l1AbsSumNeon(tmp, n)
	}
	_ = sink
}

func BenchmarkL1AbsSumScalar(b *testing.B) {
	const n = 480
	rng := rand.New(rand.NewSource(7))
	tmp := make([]float32, n)
	for i := range tmp {
		tmp[i] = float32(rng.NormFloat64())
	}
	var sink float32
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink += scalarL1Abs(tmp, n)
	}
	_ = sink
}
