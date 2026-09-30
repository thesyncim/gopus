//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/opusmath"
)

func sseOrderTestVec(rng *rand.Rand, n int) []float32 {
	v := make([]float32, n)
	for i := range v {
		switch rng.Intn(32) {
		case 0:
			v[i] = float32(math.Copysign(0, float64(rng.Float32()-0.5)))
		case 1:
			v[i] = math.Float32frombits(rng.Uint32() & 0x807fffff) // subnormal
		default:
			v[i] = (rng.Float32()*2 - 1) * float32(math.Pow(2, float64(rng.Intn(40)-20)))
		}
	}
	return v
}

func prefilterDualInnerProdSSEOrderFMAReference(x, y1, y2 []float32, length int) (float32, float32) {
	var acc1, acc2 [4]float32
	i := 0
	for ; i+4 <= length; i += 4 {
		for lane := range 4 {
			acc1[lane] = opusmath.FMA32(x[i+lane], y1[i+lane], acc1[lane])
			acc2[lane] = opusmath.FMA32(x[i+lane], y2[i+lane], acc2[lane])
		}
	}
	sum1 := add32(add32(acc1[0], acc1[2]), add32(acc1[1], acc1[3]))
	sum2 := add32(add32(acc2[0], acc2[2]), add32(acc2[1], acc2[3]))
	for ; i < length; i++ {
		sum1 = opusmath.FMA32(x[i], y1[i], sum1)
		sum2 = opusmath.FMA32(x[i], y2[i], sum2)
	}
	return sum1, sum2
}

// TestInnerProdSSEOrderSIMDBitExact pins the archsimd SSE-order inner products
// to their scalar references bit-for-bit, including tails and signed zeros.
func TestInnerProdSSEOrderSIMDBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, n := range []int{1, 3, 4, 5, 7, 8, 10, 15, 16, 60, 96, 240, 241, 480, 483, 960} {
		for trial := range 20 {
			x := sseOrderTestVec(rng, n)
			y1 := sseOrderTestVec(rng, n)
			y2 := sseOrderTestVec(rng, n)
			got := innerProdFloat32SSEOrder(x, y1, n)
			want := innerProdFloat32SSEOrderScalar(x, y1, n)
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("inner n=%d trial=%d: got %08x want %08x", n, trial,
					math.Float32bits(got), math.Float32bits(want))
			}
			g1, g2 := prefilterDualInnerProdF32SSEOrder(x, y1, y2, n)
			w1, w2 := prefilterDualInnerProdF32SSEOrderScalar(x, y1, y2, n)
			if prefilterDualInnerProdSSEUsesFMA {
				w1, w2 = prefilterDualInnerProdSSEOrderFMAReference(x, y1, y2, n)
			}
			if math.Float32bits(g1) != math.Float32bits(w1) || math.Float32bits(g2) != math.Float32bits(w2) {
				t.Fatalf("dual n=%d trial=%d: got (%08x,%08x) want (%08x,%08x)", n, trial,
					math.Float32bits(g1), math.Float32bits(g2), math.Float32bits(w1), math.Float32bits(w2))
			}
		}
	}
}

func TestInnerProdSSEOrderSIMDNoAllocs(t *testing.T) {
	x := make([]float32, 480)
	y := make([]float32, 480)
	allocs := testing.AllocsPerRun(100, func() {
		_ = innerProdFloat32SSEOrder(x, y, 480)
		_, _ = prefilterDualInnerProdF32SSEOrder(x, y, y, 480)
	})
	if allocs != 0 {
		t.Fatalf("allocs = %v, want 0", allocs)
	}
}

func BenchmarkPrefilterDualInnerProdSSEOrder(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	x, y1, y2 := make([]float32, 480), make([]float32, 480), make([]float32, 480)
	for i := range x {
		x[i], y1[i], y2[i] = rng.Float32()*2-1, rng.Float32()*2-1, rng.Float32()*2-1
	}
	b.Run("simd", func(b *testing.B) {
		for b.Loop() {
			_, _ = prefilterDualInnerProdF32SSEOrder(x, y1, y2, 480)
		}
	})
	b.Run("scalar", func(b *testing.B) {
		for b.Loop() {
			_, _ = prefilterDualInnerProdF32SSEOrderScalar(x, y1, y2, 480)
		}
	})
}
