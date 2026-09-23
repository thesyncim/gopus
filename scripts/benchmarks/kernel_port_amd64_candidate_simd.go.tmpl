//go:build amd64 && goexperiment.simd && !nosimd

package celt

import "testing"

var kernelPortBenchAMD64Recip [4]float32

func BenchmarkPortAMD64PVQRcpApprox4(b *testing.B) {
	src := [4]float32{0.79, 1.25, 3.75, 17.0}
	var dst [4]float32
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		x86RcpApprox4(&dst, &src)
	}
	b.StopTimer()
	kernelPortBenchAMD64Recip = dst
}

func BenchmarkPortAMD64PVQBestID(b *testing.B) {
	const n = 48
	absX, y := make([]float32, n), make([]float32, n)
	for i := range absX {
		absX[i] = float32((i*7)%19+1) * 0.125
		y[i] = float32((i*3)%11) * 0.25
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kernelPortBenchAMD64ID = x86PVQSearchBestIDSSE2(absX, y, 3.25, 9.5, n)
	}
}
