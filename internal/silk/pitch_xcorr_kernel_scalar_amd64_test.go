//go:build amd64 && !goexperiment.simd && !nosimd && !purego

package silk

import (
	"math"
	"testing"
)

func TestSilkPitchXcorrAVX2TinyFirstLaneEdgeValues(t *testing.T) {
	values := []float32{
		0, math.Float32frombits(1 << 31),
		math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32,
		0.5, -0.5, 1, -1,
		float32(math.Inf(1)), float32(math.Inf(-1)), math.Float32frombits(0x7fc01234),
	}
	for _, length := range []int{1, 5, 8, 9, 10, 15} {
		x := make([]float32, length)
		y := make([]float32, length+7)
		for i := range x {
			x[i] = values[i%len(values)]
		}
		for i := range y {
			y[i] = values[(i*3+1)%len(values)]
		}
		want := xcorrKernelAVX8Reference(x, y, length)
		var got [8]float32
		xcorrKernelAVX8(&x[0], &y[0], &got, length)
		for corr := range 8 {
			if math.Float32bits(got[corr]) != math.Float32bits(want[corr]) {
				t.Fatalf("length=%d corr=%d: got %08x want %08x", length, corr, math.Float32bits(got[corr]), math.Float32bits(want[corr]))
			}
		}
	}

	x := []float32{1, 2, 3, 4, 5}
	y := []float32{5, 4, 3, 2, 1, 0, -1, -2, -3, -4, -5, -6}
	var sum [8]float32
	xcorrKernelAVX8(&x[0], &y[0], &sum, len(x))
	if allocs := testing.AllocsPerRun(100, func() {
		xcorrKernelAVX8(&x[0], &y[0], &sum, len(x))
	}); allocs != 0 {
		t.Fatalf("tiny SILK xcorr kernel allocated %v times", allocs)
	}
}
