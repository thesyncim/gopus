//go:build amd64

package celt

import "testing"

var kernelPortBenchAMD64F32 float32
var kernelPortBenchAMD64ID int
var kernelPortBenchAMD64Cpx kissCpx

const kernelPortBenchAMD64Batch = 64

func kernelPortBenchAMD64Float32(n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32((i*31)%127-63) * 0.0078125
	}
	return x
}

func kernelPortBenchAMD64Complex(n int) []kissCpx {
	x := make([]kissCpx, n)
	for i := range x {
		x[i] = kissCpx{r: float32((i*17)%113-56) * 0.015625, i: float32((i*29)%109-54) * 0.015625}
	}
	return x
}

func BenchmarkPortAMD64CeltInnerProdSSEStyle(b *testing.B) {
	const n = 480
	x, y := make([]celtNorm, n), make([]celtNorm, n)
	for i := range x {
		x[i] = celtNorm(float32((i*19)%127-63) * 0.0078125)
		y[i] = celtNorm(float32((i*23)%127-63) * 0.0078125)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kernelPortBenchAMD64F32 = celtInnerProdSSEStyle(x, y)
	}
}

func benchmarkPortAMD64KfBflyInner(b *testing.B, radix int, fn func([]kissCpx, []kissCpx, int, int, int, int)) {
	const m, n, fstride = 8, 4, 8
	mm := radix * m
	initial := kernelPortBenchAMD64Complex(n * mm)
	work := make([][]kissCpx, kernelPortBenchAMD64Batch)
	for i := range work {
		work[i] = make([]kissCpx, len(initial))
	}
	w := kernelPortBenchAMD64Complex((radix*m-1)*fstride + 1)
	b.ReportAllocs()
	b.ResetTimer()
	for offset := 0; offset < b.N; offset += kernelPortBenchAMD64Batch {
		count := min(kernelPortBenchAMD64Batch, b.N-offset)
		b.StopTimer()
		for i := 0; i < count; i++ {
			copy(work[i], initial)
		}
		b.StartTimer()
		for i := 0; i < count; i++ {
			fn(work[i], w, m, n, mm, fstride)
		}
	}
	b.StopTimer()
	kernelPortBenchAMD64Cpx = work[(b.N-1)%kernelPortBenchAMD64Batch][len(initial)-1]
}

func BenchmarkPortAMD64KfBfly3Inner(b *testing.B) {
	benchmarkPortAMD64KfBflyInner(b, 3, kfBfly3Inner)
}

func BenchmarkPortAMD64KfBfly4Inner(b *testing.B) {
	benchmarkPortAMD64KfBflyInner(b, 4, kfBfly4Inner)
}

func BenchmarkPortAMD64KfBfly5Inner(b *testing.B) {
	benchmarkPortAMD64KfBflyInner(b, 5, kfBfly5Inner)
}

func BenchmarkPortAMD64PVQSearchPulseLoop(b *testing.B) {
	const n, pulses = 48, 16
	absX := make([]float32, n)
	yInitial := kernelPortBenchAMD64Float32(n)
	for i := range yInitial {
		absX[i] = float32((i*7)%19+1) * 0.125
		yInitial[i] = float32((i * 3) % 6)
	}
	workY := make([][]float32, kernelPortBenchAMD64Batch)
	workIY := make([][]int32, kernelPortBenchAMD64Batch)
	for i := range workY {
		workY[i] = make([]float32, n)
		workIY[i] = make([]int32, n)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for offset := 0; offset < b.N; offset += kernelPortBenchAMD64Batch {
		count := min(kernelPortBenchAMD64Batch, b.N-offset)
		b.StopTimer()
		for i := 0; i < count; i++ {
			copy(workY[i], yInitial)
			clear(workIY[i])
		}
		b.StartTimer()
		for i := 0; i < count; i++ {
			kernelPortBenchAMD64F32, _ = pvqSearchPulseLoop(absX, workY[i], workIY[i], 3.25, 9.5, n, pulses)
		}
	}
	b.StopTimer()
}

func BenchmarkPortAMD64ToneLPCCorr(b *testing.B) {
	const n = 480
	x := kernelPortBenchAMD64Float32(n + 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kernelPortBenchAMD64F32, _, _ = toneLPCCorr(x, n, 1, 2)
	}
}
