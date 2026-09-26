//go:build arm64

package celt

import "testing"

var kernelPortBenchF32 float32
var kernelPortBenchU32 uint32
var kernelPortBenchCpx kissCpx

const kernelPortBenchBatch = 64

func kernelPortBenchFloat32(n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32((i*37)%127-63) * 0.0078125
	}
	return x
}

func kernelPortBenchComplex(n int) []kissCpx {
	x := make([]kissCpx, n)
	for i := range x {
		x[i] = kissCpx{
			r: float32((i*17)%113-56) * 0.015625,
			i: float32((i*29)%109-54) * 0.015625,
		}
	}
	return x
}

func BenchmarkPortCombFilterConstNeon(b *testing.B) {
	const n = 480
	initial := kernelPortBenchFloat32(n)
	delay := kernelPortBenchFloat32(n + 4)
	work := make([][]float32, kernelPortBenchBatch)
	for i := range work {
		work[i] = make([]float32, n)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for offset := 0; offset < b.N; offset += kernelPortBenchBatch {
		count := min(kernelPortBenchBatch, b.N-offset)
		b.StopTimer()
		for i := 0; i < count; i++ {
			copy(work[i], initial)
		}
		b.StartTimer()
		for i := 0; i < count; i++ {
			combFilterConstNeon(work[i], delay, 0.15, -0.08, 0.03, n/4)
		}
	}
	b.StopTimer()
	kernelPortBenchF32 = work[(b.N-1)%kernelPortBenchBatch][n-1]
}

func BenchmarkPortCwrsiFastCore(b *testing.B) {
	const n, k = 48, 12
	y := make([]int, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kernelPortBenchU32 = cwrsiFastCore(n, k, 1, y)
	}
	kernelPortBenchU32 ^= uint32(y[n-1])
}

func BenchmarkPortDeemphasisStereoPlanarF32Core(b *testing.B) {
	const n = 480
	left := kernelPortBenchFloat32(n)
	right := kernelPortBenchFloat32(n)
	dst := make([]float32, n*2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kernelPortBenchF32, _ = deemphasisStereoPlanarF32Core(dst, left, right, n, 1, 0, 0, 0.85, 1e-15)
	}
	kernelPortBenchF32 += dst[n*2-1]
}

func BenchmarkPortKfBfly4M1Core(b *testing.B) {
	const n = 128
	initial := kernelPortBenchComplex(n * 4)
	work := make([][]kissCpx, kernelPortBenchBatch)
	for i := range work {
		work[i] = make([]kissCpx, len(initial))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for offset := 0; offset < b.N; offset += kernelPortBenchBatch {
		count := min(kernelPortBenchBatch, b.N-offset)
		b.StopTimer()
		for i := 0; i < count; i++ {
			copy(work[i], initial)
		}
		b.StartTimer()
		for i := 0; i < count; i++ {
			kfBfly4M1Core(work[i], n)
		}
	}
	b.StopTimer()
	kernelPortBenchCpx = work[(b.N-1)%kernelPortBenchBatch][len(initial)-1]
}

func benchmarkPortKfBflyInner(b *testing.B, radix int, fn func([]kissCpx, []kissCpx, int, int, int, int)) {
	const m, n, fstride = 8, 4, 8
	mm := radix * m
	initial := kernelPortBenchComplex(n * mm)
	work := make([][]kissCpx, kernelPortBenchBatch)
	for i := range work {
		work[i] = make([]kissCpx, len(initial))
	}
	w := kernelPortBenchComplex((radix*m-1)*fstride + 1)
	b.ReportAllocs()
	b.ResetTimer()
	for offset := 0; offset < b.N; offset += kernelPortBenchBatch {
		count := min(kernelPortBenchBatch, b.N-offset)
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
	kernelPortBenchCpx = work[(b.N-1)%kernelPortBenchBatch][len(initial)-1]
}

func BenchmarkPortKfBfly3Inner(b *testing.B) {
	benchmarkPortKfBflyInner(b, 3, kfBfly3Inner)
}

func BenchmarkPortKfBfly4Inner(b *testing.B) {
	benchmarkPortKfBflyInner(b, 4, kfBfly4Inner)
}

func BenchmarkPortKfBfly5Inner(b *testing.B) {
	benchmarkPortKfBflyInner(b, 5, kfBfly5Inner)
}

func benchmarkPortMDCTFold(b *testing.B, fn func([]kissCpx, []int, []float32, []float32, []float32, int, int, int, int, int, int, int, int, float32)) {
	const n4, n2, blocks = 64, 128, 8
	dst := kernelPortBenchComplex(n4)
	bitrev := make([]int, n4)
	for i := range bitrev {
		bitrev[i] = i
	}
	samples := kernelPortBenchFloat32(384)
	window := kernelPortBenchFloat32(256)
	trig := kernelPortBenchFloat32(n4 * 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn(dst, bitrev, samples, window, trig, 0, n4, n2, 128, 220, 20, 200, blocks, 0.5)
	}
	kernelPortBenchCpx = dst[blocks*4-1]
}

func BenchmarkPortMDCTFold1StoreNeon(b *testing.B) {
	benchmarkPortMDCTFold(b, mdctFold1StoreNeon)
}

func BenchmarkPortMDCTFold3StoreNeon(b *testing.B) {
	benchmarkPortMDCTFold(b, mdctFold3StoreNeon)
}

func BenchmarkPortMDCTMidFoldStoreNeon(b *testing.B) {
	const n4, blocks = 64, 8
	dst := kernelPortBenchComplex(n4)
	bitrev := make([]int, n4)
	for i := range bitrev {
		bitrev[i] = i
	}
	samples := kernelPortBenchFloat32(320)
	trig := kernelPortBenchFloat32(n4 * 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mdctMidFoldStoreNeon(dst, bitrev, samples, trig, 0, n4, 80, 220, blocks, 0.5)
	}
	kernelPortBenchCpx = dst[blocks*4-1]
}

func BenchmarkPortMDCTPostTwiddleNeon(b *testing.B) {
	const n4 = 64
	n2 := 2 * n4
	coeffs := kernelPortBenchFloat32(n2)
	fftStage := kernelPortBenchComplex(n4)
	trig := kernelPortBenchFloat32(n2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mdctPostTwiddleNeon(coeffs, fftStage, trig, n2, n4, n4/8)
	}
	kernelPortBenchF32 = coeffs[n2-1]
}

func BenchmarkPortPVQSearchPulseLoop(b *testing.B) {
	const n, pulses = 48, 16
	absX := kernelPortBenchFloat32(n)
	yInitial := kernelPortBenchFloat32(n)
	workY := make([][]float32, kernelPortBenchBatch)
	workIY := make([][]int32, kernelPortBenchBatch)
	for i := range workY {
		workY[i] = make([]float32, n)
		workIY[i] = make([]int32, n)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for offset := 0; offset < b.N; offset += kernelPortBenchBatch {
		count := min(kernelPortBenchBatch, b.N-offset)
		b.StopTimer()
		for i := 0; i < count; i++ {
			copy(workY[i], yInitial)
			clear(workIY[i])
		}
		b.StartTimer()
		for i := 0; i < count; i++ {
			kernelPortBenchF32, _ = pvqSearchPulseLoop(absX, workY[i], workIY[i], 3.25, 9.5, n, pulses)
		}
	}
}

func BenchmarkPortToneLPCCorr(b *testing.B) {
	const n = 480
	x := kernelPortBenchFloat32(n + 2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kernelPortBenchF32, _, _ = toneLPCCorr(x, n, 1, 2)
	}
}
