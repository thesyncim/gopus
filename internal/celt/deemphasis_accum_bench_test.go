package celt

import (
	"math"
	"runtime"
	"testing"
)

var deemphasisAccumBenchSink float32

func TestDeemphasisAccumMonoN480DoesNotAllocate(t *testing.T) {
	const n = 480
	x := make([]float32, n)
	y := make([]float32, n)
	for i := range n {
		x[i] = float32(math.Sin(float64(i+3)*0.13) * 810)
		y[i] = float32(math.Cos(float64(i+7)*0.07) * 0.1)
	}
	dec := NewDecoder(1)
	run := func() {
		dec.preemphState[0] = -71.875
		dec.deemphasis(y, x, nil, 1, n, 1, true)
	}
	run()
	if got := testing.AllocsPerRun(100, run); got != 0 {
		t.Fatalf("deemphasis accumulation allocs/run = %v, want 0", got)
	}
}

func BenchmarkDeemphasisAccumMonoN480(b *testing.B) {
	const n = 480
	x := make([]float32, n)
	y := make([]float32, n)
	for i := range n {
		x[i] = float32(math.Sin(float64(i+3)*0.13) * 810)
		y[i] = float32(math.Cos(float64(i+7)*0.07) * 0.1)
	}
	dec := NewDecoder(1)
	b.ReportAllocs()
	b.SetBytes(n * 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec.preemphState[0] = -71.875
		dec.deemphasis(y, x, nil, 1, n, 1, true)
		deemphasisAccumBenchSink = y[n-1] + dec.preemphState[0]
	}
	runtime.KeepAlive(x)
	runtime.KeepAlive(y)
}
