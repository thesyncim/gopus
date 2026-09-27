//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestKfBfly2M4SIMDMatchesScalar checks the vector m == 4 radix-2 stage
// against the scalar loop, including signed zeros and infinities.
func TestKfBfly2M4SIMDMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x2b4))
	pick := func() float32 {
		switch rng.Intn(12) {
		case 0:
			return float32(math.Copysign(0, float64(rng.Float32()-0.5)))
		case 1:
			return float32(math.Inf(1 - 2*rng.Intn(2)))
		default:
			return float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(12)-4)))
		}
	}
	for _, n := range []int{1, 2, 3, 15, 30, 60} {
		for iter := 0; iter < 50; iter++ {
			got := make([]kissCpx, 8*n)
			for i := range got {
				got[i] = kissCpx{pick(), pick()}
			}
			want := append([]kissCpx(nil), got...)
			kfBfly2M4SIMD(got, n)
			kfBfly2M4Scalar(want, n)
			for k := range want {
				if math.Float32bits(got[k].r) != math.Float32bits(want[k].r) ||
					math.Float32bits(got[k].i) != math.Float32bits(want[k].i) {
					t.Fatalf("n=%d iter %d: fout[%d] = (%08x,%08x), want (%08x,%08x)", n, iter, k,
						math.Float32bits(got[k].r), math.Float32bits(got[k].i),
						math.Float32bits(want[k].r), math.Float32bits(want[k].i))
				}
			}
		}
	}
	fout := make([]kissCpx, 8*60)
	if allocs := testing.AllocsPerRun(100, func() { kfBfly2M4SIMD(fout, 60) }); allocs != 0 {
		t.Fatalf("steady-state allocations = %g, want 0", allocs)
	}
}
