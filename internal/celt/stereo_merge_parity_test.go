package celt

import (
	"math"
	"testing"
)

// stereoMergeRescaleRef is the scalar reference for the mid/side rescale: each
// lane is l=mid*x, x=lgain*(l-y), y=rgain*(l+y) with bare noFMA32 ops (the
// product rounds to float32 before the add/sub, two roundings). Whatever
// stereoMergeRescaleNEON the build selects must reproduce this bit-for-bit.
func stereoMergeRescaleRef(x, y []float32, mid, lgain, rgain float32) {
	for i := range x {
		l := noFMA32Mul(mid, x[i])
		r := y[i]
		x[i] = noFMA32Mul(lgain, noFMA32Sub(l, r))
		y[i] = noFMA32Mul(rgain, noFMA32Add(l, r))
	}
}

func TestStereoMergeRescaleBitExact(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 120, 480}
	type gainSet struct{ mid, lgain, rgain float32 }
	gains := []gainSet{
		{1, 1, 1},
		{0.5, 0.5, 0.5},
		{1, 0.70710677, 0.70710677},
		{0.3, 1.7, -0.9},
		{-1.25, 1e-3, 1e3},
		{float32(math.Sqrt2), -0.333, 0.777},
	}
	for _, n := range lengths {
		for gi, g := range gains {
			seed := uint64(n)*1000003 + uint64(gi)*131 + 1
			x := make([]float32, n)
			y := make([]float32, n)
			for i := range x {
				x[i] = scaleParityF32(seed, i)
				y[i] = scaleParityF32(seed^0x9e3779b97f4a7c15, i)
			}
			xRef := append([]float32(nil), x...)
			yRef := append([]float32(nil), y...)
			stereoMergeRescaleRef(xRef, yRef, g.mid, g.lgain, g.rgain)
			stereoMergeRescaleNEON(x, y, g.mid, g.lgain, g.rgain)
			for i := range n {
				if math.Float32bits(x[i]) != math.Float32bits(xRef[i]) {
					t.Fatalf("x n=%d gains=%v i=%d: got %#08x want %#08x",
						n, g, i, math.Float32bits(x[i]), math.Float32bits(xRef[i]))
				}
				if math.Float32bits(y[i]) != math.Float32bits(yRef[i]) {
					t.Fatalf("y n=%d gains=%v i=%d: got %#08x want %#08x",
						n, g, i, math.Float32bits(y[i]), math.Float32bits(yRef[i]))
				}
			}
		}
	}
}

var stereoMergeBenchSink float32

// benchmarkStereoMerge times the build-selected kernel (true) or the scalar
// reference (false). The map runs in place; norm-preserving gains (mid=1,
// lgain=rgain=1/√2) keep magnitudes bounded across iterations.
func benchmarkStereoMerge(b *testing.B, n int, kernel bool) {
	x := make([]float32, n)
	y := make([]float32, n)
	for i := range x {
		x[i] = scaleParityF32(uint64(n)+1, i)
		y[i] = scaleParityF32(uint64(n)+7, i)
	}
	const mid, lgain, rgain = 1.0, 0.70710677, 0.70710677
	b.SetBytes(int64(n * 8))
	b.ResetTimer()
	if kernel {
		for range b.N {
			stereoMergeRescaleNEON(x, y, mid, lgain, rgain)
		}
	} else {
		for range b.N {
			stereoMergeRescaleRef(x, y, mid, lgain, rgain)
		}
	}
	stereoMergeBenchSink = x[n-1] + y[n-1]
}

func BenchmarkStereoMergeKernelN8(b *testing.B)   { benchmarkStereoMerge(b, 8, true) }
func BenchmarkStereoMergeRefN8(b *testing.B)      { benchmarkStereoMerge(b, 8, false) }
func BenchmarkStereoMergeKernelN16(b *testing.B)  { benchmarkStereoMerge(b, 16, true) }
func BenchmarkStereoMergeRefN16(b *testing.B)     { benchmarkStereoMerge(b, 16, false) }
func BenchmarkStereoMergeKernelN64(b *testing.B)  { benchmarkStereoMerge(b, 64, true) }
func BenchmarkStereoMergeRefN64(b *testing.B)     { benchmarkStereoMerge(b, 64, false) }
func BenchmarkStereoMergeKernelN176(b *testing.B) { benchmarkStereoMerge(b, 176, true) }
func BenchmarkStereoMergeRefN176(b *testing.B)    { benchmarkStereoMerge(b, 176, false) }
func BenchmarkStereoMergeKernelN480(b *testing.B) { benchmarkStereoMerge(b, 480, true) }
func BenchmarkStereoMergeRefN480(b *testing.B)    { benchmarkStereoMerge(b, 480, false) }
