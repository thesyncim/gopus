//go:build amd64 && goexperiment.simd && !nosimd

package silk

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func TestSilkXcorrKernelAVX8OnePassBitExact(t *testing.T) {
	if !silkUsePitchXcorrAVX2FMA {
		t.Skip("AVX2/FMA unavailable")
	}
	rng := rand.New(rand.NewSource(0x8acc))
	for _, length := range []int{16, 17, 23, 31, 32, 64, 65, 119, 120, 127, 128, 239, 240, 241, 479, 480, 481} {
		x := make([]float32, length)
		y := make([]float32, length+7)
		for trial := 0; trial < 32; trial++ {
			for i := range x {
				x[i] = float32(rng.NormFloat64())
			}
			for i := range y {
				y[i] = float32(rng.NormFloat64())
			}
			want := xcorrKernelAVX8Reference(x, y, length)
			var split, onePass, sixPlusTwo [8]float32
			xcorrKernelAVX8(&x[0], &y[0], &split, length)
			xcorrKernelAVX8OnePass(&x[0], &y[0], &onePass, length)
			xcorrKernelAVX8SixPlusTwo(&x[0], &y[0], &sixPlusTwo, length)
			for corr := range 8 {
				if got, expected := math.Float32bits(onePass[corr]), math.Float32bits(want[corr]); got != expected {
					t.Fatalf("length=%d trial=%d corr=%d: one-pass=%08x scalar=%08x", length, trial, corr, got, expected)
				}
				if got, expected := math.Float32bits(onePass[corr]), math.Float32bits(split[corr]); got != expected {
					t.Fatalf("length=%d trial=%d corr=%d: one-pass=%08x split=%08x", length, trial, corr, got, expected)
				}
				if got, expected := math.Float32bits(sixPlusTwo[corr]), math.Float32bits(want[corr]); got != expected {
					t.Fatalf("length=%d trial=%d corr=%d: six-plus-two=%08x scalar=%08x", length, trial, corr, got, expected)
				}
			}
		}
	}
}

func TestSilkXcorrKernelAVX8OnePassExceptionalParityAndZeroAlloc(t *testing.T) {
	if !silkUsePitchXcorrAVX2FMA {
		t.Skip("AVX2/FMA unavailable")
	}
	values := []float32{
		0, math.Float32frombits(1 << 31), math.SmallestNonzeroFloat32,
		-math.SmallestNonzeroFloat32, 0.5, -0.5, 1, -1,
		float32(math.Inf(1)), float32(math.Inf(-1)), math.Float32frombits(0x7fc01234),
	}
	for _, length := range []int{17, 31, 240, 241} {
		x := make([]float32, length)
		y := make([]float32, length+7)
		for i := range x {
			x[i] = values[(i*5+1)%len(values)]
		}
		for i := range y {
			y[i] = values[(i*7+3)%len(values)]
		}
		var split, onePass, sixPlusTwo [8]float32
		xcorrKernelAVX8(&x[0], &y[0], &split, length)
		xcorrKernelAVX8OnePass(&x[0], &y[0], &onePass, length)
		xcorrKernelAVX8SixPlusTwo(&x[0], &y[0], &sixPlusTwo, length)
		for corr := range 8 {
			if got, expected := math.Float32bits(onePass[corr]), math.Float32bits(split[corr]); got != expected {
				t.Fatalf("length=%d corr=%d: one-pass=%08x split=%08x", length, corr, got, expected)
			}
			if got, expected := math.Float32bits(sixPlusTwo[corr]), math.Float32bits(split[corr]); got != expected {
				t.Fatalf("length=%d corr=%d: six-plus-two=%08x split=%08x", length, corr, got, expected)
			}
		}
	}

	const length = 240
	x := make([]float32, length)
	y := make([]float32, length+7)
	for i := range x {
		x[i] = float32(i%13-6) * 0.03125
	}
	for i := range y {
		y[i] = float32(i%17-8) * 0.0625
	}
	var sum [8]float32
	xcorrKernelAVX8OnePass(&x[0], &y[0], &sum, length)
	if allocs := testing.AllocsPerRun(100, func() {
		xcorrKernelAVX8OnePass(&x[0], &y[0], &sum, length)
	}); allocs != 0 {
		t.Fatalf("one-pass xcorr allocated %v times", allocs)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		xcorrKernelAVX8SixPlusTwo(&x[0], &y[0], &sum, length)
	}); allocs != 0 {
		t.Fatalf("six-plus-two xcorr allocated %v times", allocs)
	}
}

func BenchmarkXcorrKernelAVX8Passes(b *testing.B) {
	for _, length := range []int{120, 240, 480} {
		x := make([]float32, length)
		y := make([]float32, length+7)
		for i := range x {
			x[i] = float32(i%29-14) * 0.03125
		}
		for i := range y {
			y[i] = float32(i%23-11) * 0.0625
		}
		b.Run(fmt.Sprintf("N%d/CurrentSplit", length), func(b *testing.B) {
			var sum [8]float32
			b.ReportAllocs()
			for b.Loop() {
				xcorrKernelAVX8(&x[0], &y[0], &sum, length)
				xcorrKernelAVX8BenchmarkSink = sum
			}
		})
		b.Run(fmt.Sprintf("N%d/OnePass", length), func(b *testing.B) {
			var sum [8]float32
			b.ReportAllocs()
			for b.Loop() {
				xcorrKernelAVX8OnePass(&x[0], &y[0], &sum, length)
				xcorrKernelAVX8BenchmarkSink = sum
			}
		})
		b.Run(fmt.Sprintf("N%d/SixPlusTwo", length), func(b *testing.B) {
			var sum [8]float32
			b.ReportAllocs()
			for b.Loop() {
				xcorrKernelAVX8SixPlusTwo(&x[0], &y[0], &sum, length)
				xcorrKernelAVX8BenchmarkSink = sum
			}
		})
	}
}
