//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"fmt"
	"math"
	"math/rand"
	"simd/archsimd"
	"testing"
	"unsafe"

	"github.com/thesyncim/gopus/internal/opusmath"
)

// xcorrKernelAVX4 and xcorrKernelAVX4Tail run four correlations per pass,
// the split form the one-pass kernel is checked against.
func xcorrKernelAVX4(x, y *float32, sum *[4]float32, length int) {
	tail := newXcorrTail8(unsafe.Pointer(x), length)
	xcorrKernelAVX4Tail(x, y, sum, length, &tail)
}

func xcorrKernelAVX4Tail(x, y *float32, sum *[4]float32, length int, tail *xcorrTail8) {
	var acc0, acc1, acc2, acc3 archsimd.Float32x8
	xp, yp := unsafe.Pointer(x), unsafe.Pointer(y)
	i := 0
	for ; i+16 <= length; i += 16 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(xp))
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yp)), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 4))), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 8))), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 12))), acc3)
		xp1 := unsafe.Add(xp, 32)
		yp1 := unsafe.Add(yp, 32)
		xv = archsimd.LoadFloat32x8Array((*[8]float32)(xp1))
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yp1)), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp1, 4))), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp1, 8))), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp1, 12))), acc3)
		xp = unsafe.Add(xp, 64)
		yp = unsafe.Add(yp, 64)
	}
	for ; i+8 <= length; i += 8 {
		xv := archsimd.LoadFloat32x8Array((*[8]float32)(xp))
		acc0 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(yp)), acc0)
		acc1 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 4))), acc1)
		acc2 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 8))), acc2)
		acc3 = xv.MulAdd(archsimd.LoadFloat32x8Array((*[8]float32)(unsafe.Add(yp, 12))), acc3)
		xp = unsafe.Add(xp, 32)
		yp = unsafe.Add(yp, 32)
	}
	if i < length {
		xTail := tail.x
		acc0 = xTail.MulAdd(loadXcorrTail8(yp, tail.rem), acc0)
		acc1 = xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 4), tail.rem), acc1)
		acc2 = xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 8), tail.rem), acc2)
		acc3 = xTail.MulAdd(loadXcorrTail8(unsafe.Add(yp, 12), tail.rem), acc3)
	}
	reduceXcorrAVX8Four(acc0, acc1, acc2, acc3).StoreArray(sum)
	// Clear before the legacy-SSE NaN checks; the outer wrapper clears after this block.
	archsimd.ClearAVXUpperBits()
	for corr := range sum {
		if sum[corr] != sum[corr] {
			sum[corr] = opusmath.PitchXcorrAVX2NaNReplay(
				unsafe.Slice(x, length), unsafe.Slice(y, length+3)[corr:], length)
		}
	}
}

// reduceXcorrAVX8Lanes leaves the exact horizontal sum broadcast in all lanes.
func reduceXcorrAVX8Lanes(v archsimd.Float32x8) archsimd.Float32x8 {
	v = v.Add(v.ConcatPermute128Scalars(1, 0, v))
	v = v.ConcatAddPairsGrouped(v)
	v = v.ConcatAddPairsGrouped(v)
	return v
}

// reduceXcorrAVX8Four packs four lane-zero sums with three vector shuffles.
func reduceXcorrAVX8Four(v0, v1, v2, v3 archsimd.Float32x8) archsimd.Float32x4 {
	r0 := reduceXcorrAVX8Lanes(v0)
	r1 := reduceXcorrAVX8Lanes(v1)
	r2 := reduceXcorrAVX8Lanes(v2)
	r3 := reduceXcorrAVX8Lanes(v3)
	p01 := r0.ConcatPermuteScalarsGrouped(0, 0, 4, 4, r1)
	p23 := r2.ConcatPermuteScalarsGrouped(0, 0, 4, 4, r3)
	return p01.ConcatPermuteScalarsGrouped(0, 2, 4, 6, p23).GetLo()
}

func xcorrKernelAVX8SplitForTest(x, y *float32, sum *[8]float32, length int) {
	xcorrKernelAVX4(x, y, (*[4]float32)(unsafe.Pointer(&sum[0])), length)
	xcorrKernelAVX4(x, (*float32)(unsafe.Add(unsafe.Pointer(y), 16)), (*[4]float32)(unsafe.Pointer(&sum[4])), length)
}

func TestXcorrKernelAVX8OnePassBitExact(t *testing.T) {
	if !libopusFloatPitchXCorrUsesAVX2FMA() {
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
			want := xcorrKernelAVX8Scalar(x, y, length)
			var split, onePass [8]float32
			xcorrKernelAVX8SplitForTest(&x[0], &y[0], &split, length)
			xcorrKernelAVX8OnePass(&x[0], &y[0], &onePass, length)
			for corr := range 8 {
				if got, expected := math.Float32bits(onePass[corr]), math.Float32bits(want[corr]); got != expected {
					t.Fatalf("length=%d trial=%d corr=%d: one-pass=%08x scalar=%08x", length, trial, corr, got, expected)
				}
				if got, expected := math.Float32bits(onePass[corr]), math.Float32bits(split[corr]); got != expected {
					t.Fatalf("length=%d trial=%d corr=%d: one-pass=%08x split=%08x", length, trial, corr, got, expected)
				}
			}
		}
	}
}

func TestXcorrKernelAVX8OnePassExceptionalParityAndZeroAlloc(t *testing.T) {
	if !libopusFloatPitchXCorrUsesAVX2FMA() {
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
		var split, onePass [8]float32
		xcorrKernelAVX8SplitForTest(&x[0], &y[0], &split, length)
		xcorrKernelAVX8OnePass(&x[0], &y[0], &onePass, length)
		for corr := range 8 {
			if got, expected := math.Float32bits(onePass[corr]), math.Float32bits(split[corr]); got != expected {
				t.Fatalf("length=%d corr=%d: one-pass=%08x split=%08x", length, corr, got, expected)
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
		b.Run(fmt.Sprintf("N%d/Split", length), func(b *testing.B) {
			var sum [8]float32
			b.ReportAllocs()
			for b.Loop() {
				xcorrKernelAVX8SplitForTest(&x[0], &y[0], &sum, length)
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
	}
}
