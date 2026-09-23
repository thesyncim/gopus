//go:build amd64 && !nosimd

package silk

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"runtime"
	"testing"
)

var xcorrKernelAVX8BenchmarkSink [8]float32

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

func TestXcorrKernelRuntimeIdentity(t *testing.T) {
	pc := reflect.ValueOf(xcorrKernelAVX8).Pointer()
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		t.Fatal("runtime.FuncForPC returned nil for xcorrKernelAVX8")
	}
	file, line := fn.FileLine(pc)
	t.Logf("runtime.FuncForPC=%s source=%s:%d avx2_fma_dispatch=%t", fn.Name(), file, line, silkUsePitchXcorrAVX2FMA)
}

func BenchmarkXcorrKernelAVX8(b *testing.B) {
	for _, length := range []int{1, 2, 5, 7, 8, 9, 10, 12, 15, 16, 17, 24, 32, 64, 120, 240, 480} {
		b.Run(fmt.Sprintf("N%d", length), func(b *testing.B) {
			x := make([]float32, length)
			y := make([]float32, length+7)
			for i := range x {
				x[i] = float32(i%29-14) * 0.03125
			}
			for i := range y {
				y[i] = float32(i%23-11) * 0.0625
			}
			var sum [8]float32
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				xcorrKernelAVX8(&x[0], &y[0], &sum, length)
			}
			xcorrKernelAVX8BenchmarkSink = sum
		})
	}
}
