//go:build arm64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"math/rand"
	"testing"
)

var toneLPCCorrBenchmarkSink [3]float32

func toneLPCCorrArm64AsmReference(x []float32, cnt, delay, delay2 int) (r00, r01, r02 float32) {
	var a00, a01, a02 [4]float32
	i := 0
	for ; i+3 < cnt; i += 4 {
		for lane := range 4 {
			xi := x[i+lane]
			a00[lane] = float32(math.FMA(float64(xi), float64(xi), float64(a00[lane])))
			a01[lane] = float32(math.FMA(float64(xi), float64(x[i+lane+delay]), float64(a01[lane])))
			a02[lane] = float32(math.FMA(float64(xi), float64(x[i+lane+delay2]), float64(a02[lane])))
		}
	}
	r00 = (a00[0] + a00[1]) + (a00[2] + a00[3])
	r01 = (a01[0] + a01[1]) + (a01[2] + a01[3])
	r02 = (a02[0] + a02[1]) + (a02[2] + a02[3])
	for ; i < cnt; i++ {
		xi := x[i]
		r00 = float32(math.FMA(float64(xi), float64(xi), float64(r00)))
		r01 = float32(math.FMA(float64(xi), float64(x[i+delay]), float64(r01)))
		r02 = float32(math.FMA(float64(xi), float64(x[i+delay2]), float64(r02)))
	}
	return
}

func TestToneLPCCorrArm64MatchesAsmOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(0x4c504343))
	for _, cnt := range []int{0, 1, 2, 3, 4, 5, 7, 8, 479, 480, 481} {
		for _, delays := range [][2]int{{1, 2}, {3, 6}} {
			x := make([]float32, cnt+delays[1])
			for i := range x {
				x[i] = float32(rng.NormFloat64())
			}
			got := [3]float32{}
			got[0], got[1], got[2] = toneLPCCorr(x, cnt, delays[0], delays[1])
			want := [3]float32{}
			want[0], want[1], want[2] = toneLPCCorrArm64AsmReference(x, cnt, delays[0], delays[1])
			if math.Float32bits(got[0]) != math.Float32bits(want[0]) ||
				math.Float32bits(got[1]) != math.Float32bits(want[1]) ||
				math.Float32bits(got[2]) != math.Float32bits(want[2]) {
				t.Fatalf("cnt=%d delays=%v: got (%08x,%08x,%08x), want (%08x,%08x,%08x)",
					cnt, delays,
					math.Float32bits(got[0]), math.Float32bits(got[1]), math.Float32bits(got[2]),
					math.Float32bits(want[0]), math.Float32bits(want[1]), math.Float32bits(want[2]))
			}
		}
	}
}

func TestToneLPCCorrArm64ZeroAlloc(t *testing.T) {
	const cnt = 480
	x := make([]float32, cnt+2)
	for i := range x {
		x[i] = float32((i*37)%127-63) * 0.0078125
	}
	var sink [3]float32
	sink[0], sink[1], sink[2] = toneLPCCorr(x, cnt, 1, 2)
	if allocs := testing.AllocsPerRun(100, func() {
		sink[0], sink[1], sink[2] = toneLPCCorr(x, cnt, 1, 2)
	}); allocs != 0 {
		t.Fatalf("toneLPCCorr allocated %v times", allocs)
	}
}

func BenchmarkToneLPCCorrPaired(b *testing.B) {
	const cnt = 480
	x := make([]float32, cnt+2)
	for i := range x {
		x[i] = float32((i*37)%127-63) * 0.0078125
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		toneLPCCorrBenchmarkSink[0], toneLPCCorrBenchmarkSink[1], toneLPCCorrBenchmarkSink[2] = toneLPCCorr(x, cnt, 1, 2)
	}
}
