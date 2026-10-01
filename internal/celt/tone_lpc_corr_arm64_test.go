//go:build arm64 && goexperiment.simd && !nosimd && !purego

package celt

import "testing"

var toneLPCCorrBenchmarkSink [3]float32

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
