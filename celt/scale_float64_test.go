package celt

import (
	"math"
	"testing"
)

func TestScaleFloat64IntoMatchesGeneric(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 4, 7, 8, 15, 16, 17, 31, 32, 48, 120}
	scales := []float64{-2.5, -0.125, 0, 0.75, 1.5}

	for _, n := range lengths {
		src := make([]float64, n)
		for i := range src {
			src[i] = float64((i%17)-8) * 0.125
		}
		for _, scale := range scales {
			got := make([]float64, n)
			want := make([]float64, n)

			scaleFloat64Into(got, src, scale, n)
			for i, v := range src {
				want[i] = scale * v
			}

			for i := range got {
				if math.Float64bits(got[i]) != math.Float64bits(want[i]) {
					t.Fatalf("n=%d scale=%v idx=%d got=%0.17g want=%0.17g",
						n, scale, i, got[i], want[i])
				}
			}
		}
	}
}

var scaleFloat64BenchSink float64

func BenchmarkScaleFloat64Into48(b *testing.B) {
	src := make([]float64, 48)
	dst := make([]float64, 48)
	for i := range src {
		src[i] = float64((i%23)-11) * 0.0625
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scaleFloat64Into(dst, src, 0.875, len(src))
	}
	scaleFloat64BenchSink = dst[len(dst)-1]
}

func BenchmarkScaleFloat64Into120(b *testing.B) {
	src := make([]float64, 120)
	dst := make([]float64, 120)
	for i := range src {
		src[i] = float64((i%23)-11) * 0.0625
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scaleFloat64Into(dst, src, 0.875, len(src))
	}
	scaleFloat64BenchSink = dst[len(dst)-1]
}
