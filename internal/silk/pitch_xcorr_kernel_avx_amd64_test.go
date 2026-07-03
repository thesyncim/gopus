//go:build amd64 && !purego

package silk

import (
	"math"
	"math/rand"
	"testing"
)

func xcorrKernelAVX8Reference(x, y []float32, length int) [8]float32 {
	var sums [8][8]float32
	j := 0
	for ; j < length-7; j += 8 {
		for lane := range 8 {
			xv := x[j+lane]
			for corr := range 8 {
				sums[corr][lane] = float32(math.FMA(float64(xv), float64(y[j+lane+corr]), float64(sums[corr][lane])))
			}
		}
	}
	if j != length {
		for lane := 0; lane < length-j; lane++ {
			xv := x[j+lane]
			for corr := range 8 {
				sums[corr][lane] = float32(math.FMA(float64(xv), float64(y[j+lane+corr]), float64(sums[corr][lane])))
			}
		}
	}
	var out [8]float32
	for corr := range 8 {
		out[corr] = reduceAVX2PitchSumForTest(sums[corr])
	}
	return out
}

func reduceAVX2PitchSumForTest(sum [8]float32) float32 {
	s04 := sum[0] + sum[4]
	s15 := sum[1] + sum[5]
	s26 := sum[2] + sum[6]
	s37 := sum[3] + sum[7]
	return (s04 + s15) + (s26 + s37)
}

func TestSilkPitchXcorrAVX2KernelMatchesReference(t *testing.T) {
	if !silkUsePitchXcorrAVX2FMA {
		t.Skip("AVX2/FMA unavailable")
	}
	rng := rand.New(rand.NewSource(0xc0ffee))
	lengths := []int{1, 2, 3, 5, 7, 8, 9, 15, 16, 17, 23, 31, 32, 64, 65, 120, 233}
	for _, length := range lengths {
		x := make([]float32, length)
		y := make([]float32, length+7)
		for trial := 0; trial < 64; trial++ {
			for i := range x {
				x[i] = float32(rng.NormFloat64())
			}
			for i := range y {
				y[i] = float32(rng.NormFloat64())
			}
			want := xcorrKernelAVX8Reference(x, y, length)
			var got [8]float32
			xcorrKernelAVX8(&x[0], &y[0], &got, length)
			for c := range 8 {
				if math.Float32bits(got[c]) != math.Float32bits(want[c]) {
					t.Fatalf("length=%d trial=%d corr=%d: got %08x %.10g want %08x %.10g",
						length, trial, c,
						math.Float32bits(got[c]), got[c],
						math.Float32bits(want[c]), want[c])
				}
			}
		}
	}
}
