package celt

import (
	"math"
	"math/rand"
	"testing"
)

// celtInnerProd8FMA32Ref is the original celtInnerProdNeonStyle body, kept here
// so the asm/purego kernel can be proven bit-identical to the libopus-matching
// 4-lane FMA reference it replaced. It fuses each lane through mdctFMA32
// (single-rounding math.FMA) exactly like the kernel under test, so the
// comparison holds on every architecture: the kernel is a fused
// vfmaq_f32-shaped accumulator regardless of host (it is reached in production
// only on arm64, where mdctFMA32 maps to FMADDS). celtFloatMulAdd is not used
// here because it drops to non-fused a*b+c on non-arm64 hosts, which would not
// match the kernel's unconditional FMA and would diverge by 1 ULP.
func celtInnerProd8FMA32Ref(x, y []float32) float32 {
	var acc [4]float32
	i := 0
	for ; i < len(x)-7; i += 8 {
		for lane := range 4 {
			acc[lane] = mdctFMA32(x[i+lane], y[i+lane], acc[lane])
		}
		for lane := range 4 {
			acc[lane] = mdctFMA32(x[i+4+lane], y[i+4+lane], acc[lane])
		}
	}
	if len(x)-i >= 4 {
		for lane := range 4 {
			acc[lane] = mdctFMA32(x[i+lane], y[i+lane], acc[lane])
		}
		i += 4
	}
	sum0 := math.Float32frombits(math.Float32bits(acc[0] + acc[2]))
	sum1 := math.Float32frombits(math.Float32bits(acc[1] + acc[3]))
	sum := math.Float32frombits(math.Float32bits(sum0 + sum1))
	for ; i < len(x); i++ {
		sum = mdctFMA32(x[i], y[i], sum)
	}
	return sum
}

func TestCeltInnerProd8FMA32MatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1234abcd))
	lengths := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 11, 12, 15, 16, 17, 23, 24, 31, 32, 33, 40, 47, 48, 63, 64, 65, 96, 120, 176, 240}
	for _, n := range lengths {
		for trial := range 64 {
			x := make([]float32, n)
			y := make([]float32, n)
			for i := range x {
				// Mix of magnitudes, including normalized-band-like values.
				x[i] = (rng.Float32()*2 - 1) * float32(math.Pow(2, float64(rng.Intn(20)-10)))
				y[i] = (rng.Float32()*2 - 1) * float32(math.Pow(2, float64(rng.Intn(20)-10)))
			}
			want := celtInnerProd8FMA32Ref(x, y)
			got := celtInnerProd8FMA32(x, y, n)
			if math.Float32bits(got) != math.Float32bits(want) {
				t.Fatalf("n=%d trial=%d: got %v (0x%08x) want %v (0x%08x)",
					n, trial, got, math.Float32bits(got), want, math.Float32bits(want))
			}
		}
	}
}

var innerProdBenchSink float32

func benchmarkInnerProd(b *testing.B, n int, asm bool) {
	rng := rand.New(rand.NewSource(int64(n)))
	x := make([]float32, n)
	y := make([]float32, n)
	for i := range x {
		x[i] = rng.Float32()*2 - 1
		y[i] = rng.Float32()*2 - 1
	}
	b.ResetTimer()
	var s float32
	if asm {
		for i := 0; i < b.N; i++ {
			s += celtInnerProd8FMA32(x, y, n)
		}
	} else {
		for i := 0; i < b.N; i++ {
			s += celtInnerProd8FMA32Ref(x, y)
		}
	}
	innerProdBenchSink = s
}

func BenchmarkInnerProd8FMA32_Asm_N16(b *testing.B)  { benchmarkInnerProd(b, 16, true) }
func BenchmarkInnerProd8FMA32_Ref_N16(b *testing.B)  { benchmarkInnerProd(b, 16, false) }
func BenchmarkInnerProd8FMA32_Asm_N64(b *testing.B)  { benchmarkInnerProd(b, 64, true) }
func BenchmarkInnerProd8FMA32_Ref_N64(b *testing.B)  { benchmarkInnerProd(b, 64, false) }
func BenchmarkInnerProd8FMA32_Asm_N176(b *testing.B) { benchmarkInnerProd(b, 176, true) }
func BenchmarkInnerProd8FMA32_Ref_N176(b *testing.B) { benchmarkInnerProd(b, 176, false) }

func TestCeltInnerProd8FMA32SelfProduct(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for _, n := range []int{1, 4, 7, 8, 15, 16, 17, 32, 64, 120} {
		x := make([]float32, n)
		for i := range x {
			x[i] = rng.Float32()*2 - 1
		}
		want := celtInnerProd8FMA32Ref(x, x)
		got := celtInnerProd8FMA32(x, x, n)
		if math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("self n=%d: got 0x%08x want 0x%08x", n, math.Float32bits(got), math.Float32bits(want))
		}
	}
}
