//go:build arm64 && !nosimd

package lpcnetplc

import "testing"

var fma32BenchResult float32

func BenchmarkPortLPCNetFMA32(b *testing.B) {
	a, x, c := [64]float32{}, [64]float32{}, [64]float32{}
	for i := range a {
		a[i] = float32(i+3) * 0.013671875
		x[i] = float32(65-i) * 0.0078125
		c[i] = float32(i-31) * 0.015625
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		j := i & 63
		fma32BenchResult = fma32(a[j], x[j], c[j])
	}
}
