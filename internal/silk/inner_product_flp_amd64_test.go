//go:build amd64 && !purego

package silk

import (
	"math"
	"math/rand"
	"testing"
)

func innerProductFLPAVX2Reference(a, b []float32, length int) silkCReal {
	var acc1, acc2 [4]float64
	i := 0
	for ; i < length-7; i += 8 {
		for lane := range 4 {
			acc1[lane] = math.FMA(float64(a[i+lane]), float64(b[i+lane]), acc1[lane])
			acc2[lane] = math.FMA(float64(a[i+4+lane]), float64(b[i+4+lane]), acc2[lane])
		}
	}
	for ; i < length-3; i += 4 {
		for lane := range 4 {
			acc1[lane] = math.FMA(float64(a[i+lane]), float64(b[i+lane]), acc1[lane])
		}
	}
	for lane := range 4 {
		acc1[lane] += acc2[lane]
	}
	result := (acc1[0] + acc1[2]) + (acc1[1] + acc1[3])
	for ; i < length; i++ {
		result += float64(a[i]) * float64(b[i])
	}
	return result
}

func TestInnerProductFLPAVX2MatchesReference(t *testing.T) {
	if !silkUseInnerProductFLPAVX2FMA {
		t.Skip("AVX2/FMA unavailable")
	}
	rng := rand.New(rand.NewSource(0x51511c))
	lengths := []int{1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 32, 33, 80, 120, 255, 480, 511}
	for _, n := range lengths {
		a := make([]float32, n)
		b := make([]float32, n)
		for trial := 0; trial < 64; trial++ {
			for i := range a {
				a[i] = float32(rng.NormFloat64() * 512)
				b[i] = float32(rng.NormFloat64() * 512)
			}
			got := innerProductFLPAVX2(a, b, n)
			want := innerProductFLPAVX2Reference(a, b, n)
			if math.Float64bits(got) != math.Float64bits(want) {
				t.Fatalf("n=%d trial=%d: got %016x %.17g want %016x %.17g",
					n, trial,
					math.Float64bits(got), got,
					math.Float64bits(want), want)
			}
		}
	}
}
