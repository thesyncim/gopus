//go:build arm64

package gopus

import "testing"

var kernelPortBenchPCM16 int16
var kernelPortBenchPCMOK bool

func BenchmarkPortPCMUnitBlocks(b *testing.B) {
	const n = 480
	src := make([]float32, n)
	for i := range src {
		src[i] = float32((i*37)%191-95) / 128
	}
	dst := make([]int16, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kernelPortBenchPCMOK = convertFloat32ToInt16Unit(dst, src, n)
	}
	b.StopTimer()
	kernelPortBenchPCM16 = dst[n-1]
}

func BenchmarkPortPCMSaturatingBlocks(b *testing.B) {
	const n = 480
	src := make([]float32, n)
	for i := range src {
		src[i] = float32((i*53)%511-255) / 128
	}
	dst := make([]int16, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		convertFloat32ToInt16NoSoftClipUnit(dst, src, n)
	}
	b.StopTimer()
	kernelPortBenchPCM16 = dst[n-1]
}

func BenchmarkPortPCMSoftClipUnit(b *testing.B) {
	const n = 480
	src := make([]float32, n)
	for i := range src {
		src[i] = float32((i*37)%191-95) / 128
	}
	dst := make([]int16, n)
	mem := make([]float32, 1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		softClipAndFloat32ToInt16(dst, src, n, 1, mem)
	}
	b.StopTimer()
	kernelPortBenchPCM16 = dst[n-1]
}
