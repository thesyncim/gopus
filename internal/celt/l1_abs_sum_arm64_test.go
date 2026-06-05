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

// l1AbsSumNeonReference reproduces the exact accumulation order of the NEON
// kernel: four lane accumulators take |tmp[i]| for i grouped mod 4, a sequential
// scalar tail takes the final n%4 elements, and the lanes reduce pairwise as
// (acc0+acc1)+(acc2+acc3) before the tail is added — matching the asm's
// FADDP/FADDP/FADDS sequence. abs and add carry no fusion, so the only thing
// that could diverge is add order, which this fixes; the asm must match to the bit.
func l1AbsSumNeonReference(tmp []float32, n int) float32 {
	var acc [4]float32
	i := 0
	for ; i+4 <= n; i += 4 {
		for k := 0; k < 4; k++ {
			v := tmp[i+k]
			if v < 0 {
				v = -v
			}
			acc[k] += v
		}
	}
	var tail float32
	for ; i < n; i++ {
		v := tmp[i]
		if v < 0 {
			v = -v
		}
		tail += v
	}
	return (acc[0] + acc[1]) + (acc[2] + acc[3]) + tail
}

// TestL1AbsSumNeonBitExact checks the NEON L1 abs-sum against a reference that
// reproduces its lane-accumulation and pairwise-reduce order, bit-for-bit.
func TestL1AbsSumNeonBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, n := range []int{0, 1, 2, 3, 4, 5, 7, 8, 15, 16, 31, 64, 120, 240, 481} {
		for trial := 0; trial < 100; trial++ {
			tmp := make([]float32, n)
			for i := range tmp {
				tmp[i] = float32(rng.NormFloat64()) * float32(rng.Intn(8))
			}
			got := l1AbsSumNeon(tmp, n)
			want := l1AbsSumNeonReference(tmp, n)
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("n=%d trial=%d: neon=%08x(%v) ref=%08x(%v)", n, trial,
					math.Float32bits(got), got, math.Float32bits(want), want)
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
	if math.Float32bits(got) != math.Float32bits(want) {
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
