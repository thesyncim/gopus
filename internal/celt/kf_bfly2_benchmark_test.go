//go:build amd64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// kfBfly2M1PureGoAMD64 is the pure Go reference implementation for AMD64 benchmarks.
// This is what we're trying to beat with assembly.
func kfBfly2M1PureGoAMD64(fout []kissCpx, n int) {
	for i := 0; i < n; i++ {
		fout2 := fout[1]
		fout[1].r = fout[0].r - fout2.r
		fout[1].i = fout[0].i - fout2.i
		fout[0].r += fout2.r
		fout[0].i += fout2.i
		fout = fout[2:]
	}
}

// TestKfBfly2M1CorrectnessAMD64 verifies all AMD64 implementations produce identical results.
func TestKfBfly2M1CorrectnessAMD64(t *testing.T) {
	sizes := []int{1, 2, 3, 4, 7, 8, 15, 16, 60, 120, 240, 480, 1024}

	for _, n := range sizes {
		t.Run("", func(t *testing.T) {
			// Generate random input
			rng := rand.New(rand.NewSource(42))
			input := make([]kissCpx, n*2)
			for i := range input {
				input[i] = kissCpx{
					r: float32(rng.Float64()*2 - 1),
					i: float32(rng.Float64()*2 - 1),
				}
			}

			// Reference output using pure Go
			refOut := make([]kissCpx, len(input))
			copy(refOut, input)
			kfBfly2M1PureGoAMD64(refOut, n)

			// Test SSE2
			sse2Out := make([]kissCpx, len(input))
			copy(sse2Out, input)
			kfBfly2M1SSE2(sse2Out, n)
			checkMatchAMD64(t, "SSE2", n, refOut, sse2Out)

			// Test AVX
			avxOut := make([]kissCpx, len(input))
			copy(avxOut, input)
			kfBfly2M1AVX(avxOut, n)
			checkMatchAMD64(t, "AVX", n, refOut, avxOut)

			// Test AVX2
			avx2Out := make([]kissCpx, len(input))
			copy(avx2Out, input)
			kfBfly2M1AVX2(avx2Out, n)
			checkMatchAMD64(t, "AVX2", n, refOut, avx2Out)

			// Test the dispatcher
			dispOut := make([]kissCpx, len(input))
			copy(dispOut, input)
			kfBfly2M1(dispOut, n)
			checkMatchAMD64(t, "Dispatcher", n, refOut, dispOut)
		})
	}
}

func checkMatchAMD64(t *testing.T, name string, n int, ref, got []kissCpx) {
	t.Helper()
	var maxDiff float64
	var maxDiffIdx int
	for i := range ref {
		dr := math.Abs(float64(ref[i].r - got[i].r))
		di := math.Abs(float64(ref[i].i - got[i].i))
		if dr > maxDiff {
			maxDiff = dr
			maxDiffIdx = i
		}
		if di > maxDiff {
			maxDiff = di
			maxDiffIdx = i
		}
	}
	if maxDiff > 1e-6 {
		t.Errorf("n=%d %s: max diff = %.2e at index %d (ref: {%.6f, %.6f}, got: {%.6f, %.6f})",
			n, name, maxDiff, maxDiffIdx,
			ref[maxDiffIdx].r, ref[maxDiffIdx].i,
			got[maxDiffIdx].r, got[maxDiffIdx].i)
	}
}

// Benchmark sizes for FFT operations in CELT
var benchmarkSizesAMD64 = []int{60, 120, 240, 480}

func BenchmarkKfBfly2M1AMD64(b *testing.B) {
	for _, n := range benchmarkSizesAMD64 {
		input := make([]kissCpx, n*2)
		rng := rand.New(rand.NewSource(42))
		for i := range input {
			input[i] = kissCpx{r: float32(rng.Float64()), i: float32(rng.Float64())}
		}

		b.Run("PureGo/n="+itoaAMD64(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(data, input)
				kfBfly2M1PureGoAMD64(data, n)
			}
		})

		b.Run("SSE2/n="+itoaAMD64(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(data, input)
				kfBfly2M1SSE2(data, n)
			}
		})

		b.Run("AVX/n="+itoaAMD64(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(data, input)
				kfBfly2M1AVX(data, n)
			}
		})

		b.Run("AVX2/n="+itoaAMD64(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(data, input)
				kfBfly2M1AVX2(data, n)
			}
		})
	}
}

// BenchmarkKfBfly2M1NoCopyAMD64 benchmarks without the copy overhead
func BenchmarkKfBfly2M1NoCopyAMD64(b *testing.B) {
	for _, n := range benchmarkSizesAMD64 {
		input := make([]kissCpx, n*2)
		rng := rand.New(rand.NewSource(42))
		for i := range input {
			input[i] = kissCpx{r: float32(rng.Float64()), i: float32(rng.Float64())}
		}

		b.Run("PureGo/n="+itoaAMD64(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			copy(data, input)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				kfBfly2M1PureGoAMD64(data, n)
			}
		})

		b.Run("SSE2/n="+itoaAMD64(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			copy(data, input)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				kfBfly2M1SSE2(data, n)
			}
		})

		b.Run("AVX/n="+itoaAMD64(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			copy(data, input)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				kfBfly2M1AVX(data, n)
			}
		})

		b.Run("AVX2/n="+itoaAMD64(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			copy(data, input)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				kfBfly2M1AVX2(data, n)
			}
		})
	}
}

// BenchmarkKissFFT32AMD64 benchmarks the full FFT including the butterfly operations
func BenchmarkKissFFT32AMD64(b *testing.B) {
	for _, n := range benchmarkSizesAMD64 {
		input := make([]complex64, n)
		rng := rand.New(rand.NewSource(42))
		for i := range input {
			input[i] = complex(float32(rng.Float64()), float32(rng.Float64()))
		}

		b.Run("n="+itoaAMD64(n), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = kissFFT32(input)
			}
		})
	}
}

func itoaAMD64(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
