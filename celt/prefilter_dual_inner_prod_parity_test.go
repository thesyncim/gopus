package celt

import (
	"math"
	"math/rand"
	"testing"
)

// prefilterDualInnerProdF32Ref is the original prefilterDualInnerProdF32NeonOrder
// body, kept verbatim so the asm/purego kernel can be proven bit-identical to
// the libopus-matching reference it replaced. On arm64 fma32 and the scalar
// tail multiply-add both contract to FMADDS, matching the asm kernel.
func prefilterDualInnerProdF32Ref(x, y1, y2 []float32, length int) (float32, float32) {
	var acc1 [4]float32
	var acc2 [4]float32
	i := 0
	for ; i < length-7; i += 8 {
		acc1[0] = fma32(x[i], y1[i], acc1[0])
		acc1[1] = fma32(x[i+1], y1[i+1], acc1[1])
		acc1[2] = fma32(x[i+2], y1[i+2], acc1[2])
		acc1[3] = fma32(x[i+3], y1[i+3], acc1[3])
		acc2[0] = fma32(x[i], y2[i], acc2[0])
		acc2[1] = fma32(x[i+1], y2[i+1], acc2[1])
		acc2[2] = fma32(x[i+2], y2[i+2], acc2[2])
		acc2[3] = fma32(x[i+3], y2[i+3], acc2[3])

		acc1[0] = fma32(x[i+4], y1[i+4], acc1[0])
		acc1[1] = fma32(x[i+5], y1[i+5], acc1[1])
		acc1[2] = fma32(x[i+6], y1[i+6], acc1[2])
		acc1[3] = fma32(x[i+7], y1[i+7], acc1[3])
		acc2[0] = fma32(x[i+4], y2[i+4], acc2[0])
		acc2[1] = fma32(x[i+5], y2[i+5], acc2[1])
		acc2[2] = fma32(x[i+6], y2[i+6], acc2[2])
		acc2[3] = fma32(x[i+7], y2[i+7], acc2[3])
	}
	if length-i >= 4 {
		acc1[0] = fma32(x[i], y1[i], acc1[0])
		acc1[1] = fma32(x[i+1], y1[i+1], acc1[1])
		acc1[2] = fma32(x[i+2], y1[i+2], acc1[2])
		acc1[3] = fma32(x[i+3], y1[i+3], acc1[3])
		acc2[0] = fma32(x[i], y2[i], acc2[0])
		acc2[1] = fma32(x[i+1], y2[i+1], acc2[1])
		acc2[2] = fma32(x[i+2], y2[i+2], acc2[2])
		acc2[3] = fma32(x[i+3], y2[i+3], acc2[3])
		i += 4
	}
	xy10 := acc1[0] + acc1[2]
	xy11 := acc1[1] + acc1[3]
	xy20 := acc2[0] + acc2[2]
	xy21 := acc2[1] + acc2[3]
	sum1 := xy10 + xy11
	sum2 := xy20 + xy21
	for ; i < length; i++ {
		sum1 += x[i] * y1[i]
		sum2 += x[i] * y2[i]
	}
	return sum1, sum2
}

func TestPrefilterDualInnerProdMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x55aa55aa))
	lengths := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 11, 12, 15, 16, 17, 23, 24, 31, 32, 33, 47, 48, 63, 64, 65, 96, 120, 240, 320}
	for _, n := range lengths {
		for trial := 0; trial < 64; trial++ {
			x := make([]float32, n)
			y1 := make([]float32, n)
			y2 := make([]float32, n)
			for i := range x {
				x[i] = (rng.Float32()*2 - 1) * float32(math.Pow(2, float64(rng.Intn(16)-8)))
				y1[i] = (rng.Float32()*2 - 1) * float32(math.Pow(2, float64(rng.Intn(16)-8)))
				y2[i] = (rng.Float32()*2 - 1) * float32(math.Pow(2, float64(rng.Intn(16)-8)))
			}
			w1, w2 := prefilterDualInnerProdF32Ref(x, y1, y2, n)
			g1, g2 := prefilterDualInnerProdAsm(x, y1, y2, n)
			if math.Float32bits(g1) != math.Float32bits(w1) || math.Float32bits(g2) != math.Float32bits(w2) {
				t.Fatalf("n=%d trial=%d: got (0x%08x,0x%08x) want (0x%08x,0x%08x)",
					n, trial, math.Float32bits(g1), math.Float32bits(g2),
					math.Float32bits(w1), math.Float32bits(w2))
			}
		}
	}
}

var dualInnerProdBenchSink float32

func benchmarkDualInnerProd(b *testing.B, n int, asm bool) {
	rng := rand.New(rand.NewSource(int64(n)))
	x := make([]float32, n)
	y1 := make([]float32, n)
	y2 := make([]float32, n)
	for i := range x {
		x[i] = rng.Float32()*2 - 1
		y1[i] = rng.Float32()*2 - 1
		y2[i] = rng.Float32()*2 - 1
	}
	b.ResetTimer()
	var s1, s2 float32
	if asm {
		for i := 0; i < b.N; i++ {
			a, c := prefilterDualInnerProdAsm(x, y1, y2, n)
			s1, s2 = a, c
		}
	} else {
		for i := 0; i < b.N; i++ {
			a, c := prefilterDualInnerProdF32Ref(x, y1, y2, n)
			s1, s2 = a, c
		}
	}
	dualInnerProdBenchSink = s1 + s2
}

func BenchmarkDualInnerProd_Asm_N240(b *testing.B) { benchmarkDualInnerProd(b, 240, true) }
func BenchmarkDualInnerProd_Ref_N240(b *testing.B) { benchmarkDualInnerProd(b, 240, false) }
