//go:build arm64 && goexperiment.simd && !nosimd

package silk

import "testing"

var kernelPortBenchSilkF32 float32
var kernelPortBenchSilkReal silkCReal
var kernelPortBenchSilkI16 int16

const kernelPortBenchSilkBatch = 64

func kernelPortBenchSilkFloat32(n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32((i*31)%127-63) * 0.0078125
	}
	return x
}

func kernelPortBenchSilkInt16(n int) []int16 {
	x := make([]int16, n)
	for i := range x {
		x[i] = int16((i*7919)%60001 - 30000)
	}
	return x
}

func BenchmarkPortFloatToInt16Scaled(b *testing.B) {
	const n = 480
	in := kernelPortBenchSilkFloat32(n)
	out := make([]int16, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		floatToInt16Scaled(out, in, 32768, n)
	}
	b.StopTimer()
	kernelPortBenchSilkI16 = out[n-1]
}

func BenchmarkPortInnerProductFLPArm64(b *testing.B) {
	const n = 480
	a := kernelPortBenchSilkFloat32(n)
	c := kernelPortBenchSilkFloat32(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kernelPortBenchSilkReal = innerProductFLPArm64(a, c, n)
	}
}

func BenchmarkPortWriteInt16AsFloat32Core(b *testing.B) {
	const n = 480
	src := kernelPortBenchSilkInt16(n)
	dst := make([]float32, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writeInt16AsFloat32Core(dst, src, n)
	}
	b.StopTimer()
	kernelPortBenchSilkF32 = dst[n-1]
}

func BenchmarkPortCeltPitchXcorrFloat(b *testing.B) {
	const length, maxPitch = 240, 120
	x := kernelPortBenchSilkFloat32(length)
	y := kernelPortBenchSilkFloat32(length + maxPitch - 1)
	out := make([]float32, maxPitch)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		celtPitchXcorrFloat(x, y, out, length, maxPitch)
	}
	b.StopTimer()
	kernelPortBenchSilkF32 = out[maxPitch-1]
}

func benchmarkPortFIRInterpol(b *testing.B, nOut, bufLen int, fn func([]int16, []int16, int)) {
	dst := make([]int16, nOut)
	buf := kernelPortBenchSilkInt16(bufLen)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn(dst, buf, nOut)
	}
	b.StopTimer()
	kernelPortBenchSilkI16 = dst[nOut-1]
}

func BenchmarkPortFIRInterpol21846Core(b *testing.B) {
	const nOut = 240
	benchmarkPortFIRInterpol(b, nOut, (nOut-1)/3+8, firInterpol21846Core)
}

func BenchmarkPortFIRInterpol32768Core(b *testing.B) {
	const nOut = 240
	benchmarkPortFIRInterpol(b, nOut, ((nOut-1)>>1)+8, firInterpol32768Core)
}

func BenchmarkPortFIRInterpol43691Core(b *testing.B) {
	const nOut = 240
	benchmarkPortFIRInterpol(b, nOut, (2*(nOut-1))/3+8, firInterpol43691Core)
}

func BenchmarkPortUp2HQCore(b *testing.B) {
	const n = 240
	in := kernelPortBenchSilkInt16(n)
	initialState := [6]int32{12345, -23456, 34567, -45678, 56789, -67890}
	out := make([]int16, 2*n)
	state := make([][6]int32, kernelPortBenchSilkBatch)
	b.ReportAllocs()
	b.ResetTimer()
	for offset := 0; offset < b.N; offset += kernelPortBenchSilkBatch {
		count := min(kernelPortBenchSilkBatch, b.N-offset)
		b.StopTimer()
		for i := 0; i < count; i++ {
			state[i] = initialState
		}
		b.StartTimer()
		for i := 0; i < count; i++ {
			up2HQCore(out, in, &state[i])
		}
	}
	b.StopTimer()
	kernelPortBenchSilkI16 = out[2*n-1]
}
