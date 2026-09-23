//go:build arm64 && !purego

package silk

import (
	"math"
	"math/rand"
	"testing"
)

func TestInnerProductFLPArm64MatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(151))
	lengths := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 31, 32, 33, 80, 120, 480}
	for _, n := range lengths {
		for trial := range 64 {
			a := make([]float32, n)
			b := make([]float32, n)
			for i := range a {
				a[i] = float32(rng.NormFloat64() * 8192)
				b[i] = float32(rng.NormFloat64() * 4096)
			}
			if trial == 0 && n >= 8 {
				a[0], a[1], a[2], a[3] = 1e20, 1, -1e20, 1
				a[4], a[5], a[6], a[7] = -1e20, 1, 1e20, 1
				for i := range 8 {
					b[i] = 1
				}
			}
			got := innerProductFLPArm64(a, b, n)
			want := innerProductF32Libopus(a, b, n)
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("n=%d trial=%d: got %016x %.17g want %016x %.17g",
					n, trial, math.Float64bits(got), got, math.Float64bits(want), want)
			}
		}
	}
}
