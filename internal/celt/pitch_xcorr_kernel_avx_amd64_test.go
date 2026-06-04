//go:build amd64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// scalar reference reproducing the lane-ordered AVX2 accumulation that the asm
// kernel must match bit-for-bit.
func xcorrKernelAVX8Scalar(x, y []float32, length int) [8]float32 {
	var sums [8][8]float32
	j := 0
	for ; j < length-7; j += 8 {
		for lane := 0; lane < 8; lane++ {
			xv := x[j+lane]
			for corr := 0; corr < 8; corr++ {
				sums[corr][lane] = float32(math.FMA(float64(xv), float64(y[j+lane+corr]), float64(sums[corr][lane])))
			}
		}
	}
	if j != length {
		for lane := 0; lane < length-j; lane++ {
			xv := x[j+lane]
			for corr := 0; corr < 8; corr++ {
				sums[corr][lane] = float32(math.FMA(float64(xv), float64(y[j+lane+corr]), float64(sums[corr][lane])))
			}
		}
	}
	var out [8]float32
	for corr := 0; corr < 8; corr++ {
		out[corr] = reduceAVX2PitchSum(sums[corr])
	}
	return out
}

func TestXcorrKernelAVX8BitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))
	for _, length := range []int{1, 2, 3, 5, 7, 8, 9, 15, 16, 17, 23, 31, 32, 64, 65, 120, 233, 720, 721} {
		x := make([]float32, length)
		y := make([]float32, length+7)
		for trial := 0; trial < 64; trial++ {
			for i := range x {
				x[i] = float32(rng.NormFloat64())
			}
			for i := range y {
				y[i] = float32(rng.NormFloat64())
			}
			want := xcorrKernelAVX8Scalar(x, y, length)
			var got [8]float32
			xcorrKernelAVX8(&x[0], &y[0], &got, length)
			for c := 0; c < 8; c++ {
				if math.Float32bits(got[c]) != math.Float32bits(want[c]) {
					t.Fatalf("length=%d trial=%d corr=%d: got %v (%#x) want %v (%#x)",
						length, trial, c, got[c], math.Float32bits(got[c]), want[c], math.Float32bits(want[c]))
				}
			}
		}
	}
}
