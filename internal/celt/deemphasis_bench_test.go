package celt

import (
	"math"
	"runtime"
	"testing"
)

var deemphasisBenchSink float32

func BenchmarkDeemphasisMonoN480(b *testing.B) {
	const n = 480
	samples := make([]float32, n)
	dst := make([]float32, n)
	for i := range samples {
		samples[i] = float32(math.Sin(float64(i+1)*0.113)*1900 + math.Cos(float64(i+7)*0.047)*720)
	}
	dec := NewDecoder(1)
	initialState := float32(-71.875)
	b.ReportAllocs()
	b.SetBytes(n * 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec.preemphState[0] = initialState
		dec.applyDeemphasisAndScaleMonoFloat32ToFloat32(dst, samples, 1.0/32768.0)
		deemphasisBenchSink = dst[n-1] + dec.preemphState[0]
	}
	runtime.KeepAlive(dst)
	runtime.KeepAlive(samples)
}

func BenchmarkDeemphasisStereoN480(b *testing.B) {
	const n = 480
	left, right := make([]float32, n), make([]float32, n)
	dst := make([]float32, n*2)
	for i := range n {
		left[i] = float32(math.Sin(float64(i+3)*0.113)*1900 + math.Cos(float64(i+9)*0.047)*720)
		right[i] = float32(math.Cos(float64(i+6)*0.151)*1600 - math.Sin(float64(i+2)*0.083)*810)
	}
	dec := NewDecoder(2)
	initialStateL, initialStateR := float32(-71.875), float32(311.5)
	b.ReportAllocs()
	b.SetBytes(n * 2 * 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec.preemphState[0] = initialStateL
		dec.preemphState[1] = initialStateR
		dec.applyDeemphasisAndScaleStereoPlanarFloat32ToFloat32(dst, left, right, 1.0/32768.0)
		deemphasisBenchSink = dst[len(dst)-1] + dec.preemphState[0] + dec.preemphState[1]
	}
	runtime.KeepAlive(dst)
	runtime.KeepAlive(left)
	runtime.KeepAlive(right)
}
