//go:build arm64

package dnnmath

import "testing"

var reciprocalEstimateBenchResult float32

func BenchmarkPortReciprocalEstimate32(b *testing.B) {
	inputs := [64]float32{}
	for i := range inputs {
		inputs[i] = 0.125 + float32(i+1)*0.03125
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reciprocalEstimateBenchResult = reciprocalEstimate32(inputs[i&63])
	}
}
