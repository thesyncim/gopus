//go:build amd64 && !nosimd

package celt

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"runtime"
	"testing"
)

var xcorrKernelAVX8BenchmarkSink [8]float32

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
	for _, length := range []int{1, 2, 3, 5, 7, 8, 9, 15, 16, 17, 23, 31, 32, 64, 65, 120, 233, 239, 240, 241, 720, 721} {
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

func TestXcorrKernelAVX8LargePathZeroAlloc(t *testing.T) {
	const length = 64
	x := make([]float32, length)
	y := make([]float32, length+7)
	for i := range x {
		x[i] = float32(i%13-6) * 0.03125
	}
	for i := range y {
		y[i] = float32(i%17-8) * 0.0625
	}
	var sum [8]float32
	xcorrKernelAVX8(&x[0], &y[0], &sum, length)
	if allocs := testing.AllocsPerRun(100, func() {
		xcorrKernelAVX8(&x[0], &y[0], &sum, length)
	}); allocs != 0 {
		t.Fatalf("large xcorr kernel allocated %v times", allocs)
	}
}

func TestXcorrKernelAVX8TinyFirstLaneEdgeValues(t *testing.T) {
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
		want := xcorrKernelAVX8Scalar(x, y, length)
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
		t.Fatalf("tiny xcorr kernel allocated %v times", allocs)
	}
}

func TestPitchXCorrAVX2FMAOrderTinyMatchesKernelGroups(t *testing.T) {
	values := []float32{
		0, math.Float32frombits(1 << 31),
		math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32,
		0.5, -0.5, 1, -1,
		float32(math.Inf(1)), float32(math.Inf(-1)), math.Float32frombits(0x7fc01234),
	}
	for _, length := range []int{1, 5, 8, 9, 10, 15} {
		for _, maxPitch := range []int{1, 2, 5, 8, 10, 244} {
			x := make([]float32, length)
			y := make([]float32, maxPitch+length+7)
			for i := range x {
				x[i] = values[i%len(values)]
			}
			for i := range y {
				y[i] = values[(i*3+1)%len(values)]
			}
			want := make([]float32, maxPitch)
			for pitch := 0; pitch < maxPitch-7; pitch += 8 {
				var sums [8]float32
				xcorrKernelAVX8(&x[0], &y[pitch], &sums, length)
				copy(want[pitch:pitch+8], sums[:])
			}
			pitch := maxPitch &^ 7
			for ; pitch < maxPitch; pitch++ {
				want[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], length)
			}
			got := make([]float32, maxPitch)
			pitchXCorrFloat32AVX2FMAOrderTiny(x, y, got, length, maxPitch)
			for i := range got {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("length=%d maxPitch=%d pitch=%d: got %08x want %08x", length, maxPitch, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}
			if libopusFloatPitchXCorrUsesAVX2FMA() {
				production := make([]float32, maxPitch)
				pitchXCorrFloat32(x, y, production, length, maxPitch)
				for i := range production {
					if math.Float32bits(production[i]) != math.Float32bits(want[i]) {
						t.Fatalf("production length=%d maxPitch=%d pitch=%d: got %08x want %08x", length, maxPitch, i, math.Float32bits(production[i]), math.Float32bits(want[i]))
					}
				}
				if allocs := testing.AllocsPerRun(100, func() {
					pitchXCorrFloat32(x, y, production, length, maxPitch)
				}); allocs != 0 {
					t.Fatalf("production length=%d maxPitch=%d allocated %v times", length, maxPitch, allocs)
				}
			}
		}
	}

	x := []float32{1, 2, 3, 4, 5}
	y := make([]float32, 5+244+7)
	out := make([]float32, 244)
	if allocs := testing.AllocsPerRun(100, func() {
		pitchXCorrFloat32AVX2FMAOrderTiny(x, y, out, len(x), len(out))
	}); allocs != 0 {
		t.Fatalf("tiny pitch xcorr allocated %v times", allocs)
	}
}

func TestPitchXCorrTinyLength10ExactAndZeroAlloc(t *testing.T) {
	const length, maxPitch = 10, 10
	rng := rand.New(rand.NewSource(2510))
	x := make([]float32, length)
	y := make([]float32, length+maxPitch)
	got := make([]float32, maxPitch)
	want := make([]float32, maxPitch)
	for trial := 0; trial < 128; trial++ {
		for i := range x {
			x[i] = float32(rng.NormFloat64())
		}
		for i := range y {
			y[i] = float32(rng.NormFloat64())
		}
		var group [8]float32
		xcorrKernelAVX8(&x[0], &y[0], &group, length)
		copy(want, group[:])
		for pitch := 8; pitch < maxPitch; pitch++ {
			want[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], length)
		}
		pitchXCorrFloat32AVX2FMAOrderTiny(x, y, got, length, maxPitch)
		for pitch := range got {
			if math.Float32bits(got[pitch]) != math.Float32bits(want[pitch]) {
				t.Fatalf("trial=%d pitch=%d: got %08x want %08x", trial, pitch, math.Float32bits(got[pitch]), math.Float32bits(want[pitch]))
			}
		}
	}
	if allocs := testing.AllocsPerRun(100, func() {
		pitchXCorrFloat32AVX2FMAOrderTiny(x, y, got, length, maxPitch)
	}); allocs != 0 {
		t.Fatalf("length-10 tiny pitch xcorr allocated %v times", allocs)
	}
}

func TestPitchXCorrTinyLength10EdgeValuesAndTailSizes(t *testing.T) {
	const length = 10
	values := []float32{
		0, math.Float32frombits(1 << 31),
		math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32,
		0.5, -0.5, 1, -1,
		float32(math.Inf(1)), float32(math.Inf(-1)),
		math.Float32frombits(0x7fc01234), math.Float32frombits(0xffc05678),
	}
	signedZeroValues := []float32{
		0, math.Float32frombits(1 << 31),
		math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32,
		0.5, -0.5, 1, -1,
	}
	for maxPitch := 1; maxPitch <= 17; maxPitch++ {
		x := make([]float32, length)
		y := make([]float32, length+maxPitch)
		for variant := 0; variant < 2; variant++ {
			inputValues := values
			if variant == 1 {
				inputValues = signedZeroValues
			}
			for i := range x {
				x[i] = inputValues[(i*5+variant*3)%len(inputValues)]
			}
			for i := range y {
				y[i] = inputValues[(i*7+variant*5)%len(inputValues)]
			}
			if variant == 0 {
				x[0], y[0] = 0, float32(math.Inf(-1))
				x[1], y[1] = 1, math.Float32frombits(0x7fc01234)
				x[2], y[2] = math.Float32frombits(1<<31), -2
			} else {
				x[0], y[0] = 0, -1
				x[1], y[1] = math.Float32frombits(1<<31), 1
				x[2], y[2] = math.SmallestNonzeroFloat32, 0.5
			}

			want := make([]float32, maxPitch)
			for pitch := 0; pitch+8 <= maxPitch; pitch += 8 {
				var group [8]float32
				xcorrKernelAVX8(&x[0], &y[pitch], &group, length)
				copy(want[pitch:pitch+8], group[:])
			}
			for pitch := maxPitch &^ 7; pitch < maxPitch; pitch++ {
				want[pitch] = innerProdFloat32SSEOrder(x, y[pitch:], length)
			}

			got := make([]float32, maxPitch)
			pitchXCorrFloat32AVX2FMAOrderTiny(x, y, got, length, maxPitch)
			for pitch := range got {
				if math.Float32bits(got[pitch]) != math.Float32bits(want[pitch]) {
					t.Fatalf("maxPitch=%d variant=%d pitch=%d: got %08x want %08x", maxPitch, variant, pitch, math.Float32bits(got[pitch]), math.Float32bits(want[pitch]))
				}
			}
		}
	}
}

func TestPitchXCorrTinyNaNAndSignedZeroMatchKernelGroup(t *testing.T) {
	x := []float32{0, math.Float32frombits(1 << 31), 1, -1, 0.5}
	y := make([]float32, len(x)+7)
	y[0] = float32(math.Inf(-1))
	y[1] = 1
	y[4] = math.Float32frombits(0x7fc01234)
	var want [8]float32
	xcorrKernelAVX8(&x[0], &y[0], &want, len(x))
	if bits := math.Float32bits(want[0]); bits != 0x7fc01234 {
		t.Fatalf("Inf-induced NaN reference bits = %08x, want payload 7fc01234", bits)
	}
	got := make([]float32, 8)
	pitchXCorrFloat32AVX2FMAOrderTiny(x, y, got, len(x), len(got))
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("NaN/signed-zero pitch=%d: got %08x want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
	if allocs := testing.AllocsPerRun(100, func() {
		pitchXCorrFloat32AVX2FMAOrderTiny(x, y, got, len(x), len(got))
	}); allocs != 0 {
		t.Fatalf("NaN/signed-zero tiny pitch xcorr allocated %v times", allocs)
	}
}

func TestXcorrKernelRuntimeIdentity(t *testing.T) {
	pc := reflect.ValueOf(xcorrKernelAVX8).Pointer()
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		t.Fatal("runtime.FuncForPC returned nil for xcorrKernelAVX8")
	}
	file, line := fn.FileLine(pc)
	t.Logf("runtime.FuncForPC=%s source=%s:%d avx2_fma_dispatch=%t", fn.Name(), file, line, libopusFloatPitchXCorrUsesAVX2FMA())
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
