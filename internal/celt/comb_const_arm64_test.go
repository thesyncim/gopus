//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestCombFilterConstNeonBitExact pins the dispatched comb body to the scalar
// rotated loop bit-for-bit across lengths that exercise the head/blocks/tail
// split and both delay-slice shapes.
func TestCombFilterConstNeonBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	for _, n := range []int{1, 4, 8, 9, 12, 17, 23, 120, 239, 240, 960} {
		for trial := 0; trial < 6; trial++ {
			g10 := rng.Float32() - 0.5
			g11 := rng.Float32() - 0.5
			g12 := rng.Float32() - 0.5
			x4, x3, x2, x1 := rng.Float32(), rng.Float32(), rng.Float32(), rng.Float32()
			base := make([]float32, n)
			delay := make([]float32, n)
			for i := range base {
				base[i] = float32(rng.NormFloat64())
				delay[i] = float32(rng.NormFloat64())
			}
			got := append([]float32(nil), base...)
			want := append([]float32(nil), base...)

			ga4, ga3, ga2, ga1 := combFilterConstFloat32(got, delay, g10, g11, g12, x4, x3, x2, x1)

			// Scalar reference: the simple rotated loop.
			w4, w3, w2, w1 := x4, x3, x2, x1
			for i := 0; i < n; i++ {
				x0 := delay[i]
				want[i] = combFilterConstValue(want[i], g10, g11, g12, w2, w1, w3, x0, w4)
				w4, w3, w2, w1 = w3, w2, w1, x0
			}

			for k := range want {
				if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
					t.Fatalf("n=%d trial=%d: dst[%d] = %08x, want %08x", n, trial, k,
						math.Float32bits(got[k]), math.Float32bits(want[k]))
				}
			}
			if ga4 != w4 || ga3 != w3 || ga2 != w2 || ga1 != w1 {
				t.Fatalf("n=%d trial=%d: carries (%v,%v,%v,%v) want (%v,%v,%v,%v)",
					n, trial, ga4, ga3, ga2, ga1, w4, w3, w2, w1)
			}
		}
	}
}
