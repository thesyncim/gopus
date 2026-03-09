package celt

import (
	"math"
	"math/rand"
	"testing"
)

func maxAbsSliceF64Legacy(x []float64) float64 {
	maxAbs := 0.0
	for _, v := range x {
		a := math.Abs(v)
		if a > maxAbs {
			maxAbs = a
		}
	}
	return maxAbs
}

func TestMaxAbsSliceF64MatchesLegacy(t *testing.T) {
	cases := [][]float64{
		nil,
		{},
		{0, -0, 0.25, -0.5, 0.125},
		{-1, 2, -3, 4, -5, 1.5},
		{math.NaN(), 0.5, -2.5, math.Inf(1), -math.Inf(1)},
		{math.NaN(), -0, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64},
	}

	rng := rand.New(rand.NewSource(1))
	for n := 1; n <= 257; n += 17 {
		buf := make([]float64, n)
		for i := range buf {
			switch i % 19 {
			case 0:
				buf[i] = math.NaN()
			case 1:
				buf[i] = math.Inf(1)
			case 2:
				buf[i] = math.Inf(-1)
			default:
				buf[i] = rng.NormFloat64() * (1 + float64(i%7))
			}
		}
		cases = append(cases, buf)
	}

	for i, tc := range cases {
		got := maxAbsSliceF64(tc)
		want := maxAbsSliceF64Legacy(tc)
		if math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("case %d: maxAbsSliceF64()=%v bits=%#x, want %v bits=%#x", i, got, math.Float64bits(got), want, math.Float64bits(want))
		}
	}
}

func BenchmarkMaxAbsSliceF64Current(b *testing.B) {
	benchMaxAbsSliceF64(b, maxAbsSliceF64)
}

func BenchmarkMaxAbsSliceF64Legacy(b *testing.B) {
	benchMaxAbsSliceF64(b, maxAbsSliceF64Legacy)
}

func benchMaxAbsSliceF64(b *testing.B, fn func([]float64) float64) {
	cases := []struct {
		name string
		n    int
	}{
		{name: "mono480", n: 480},
		{name: "stereo960", n: 1920},
		{name: "stereo2880", n: 5760},
	}

	rng := rand.New(rand.NewSource(7))
	for _, tc := range cases {
		data := make([]float64, tc.n)
		for i := range data {
			data[i] = rng.NormFloat64() * (1 + float64(i%11))
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			sum := 0.0
			for i := 0; i < b.N; i++ {
				sum += fn(data)
			}
			if sum == 0 {
				b.Fatal(sum)
			}
		})
	}
}
