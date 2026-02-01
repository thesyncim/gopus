package celt

import (
	"math"
	"math/rand"
	"testing"
)

// kfBfly2M1Reference is the pure Go reference implementation.
func kfBfly2M1Reference(fout []kissCpx, n int) {
	for i := 0; i < n; i++ {
		fout2 := fout[1]
		fout[1].r = fout[0].r - fout2.r
		fout[1].i = fout[0].i - fout2.i
		fout[0].r += fout2.r
		fout[0].i += fout2.i
		fout = fout[2:]
	}
}

// TestKfBfly2ViaFFT tests kfBfly2M1 through the actual FFT path
func TestKfBfly2ViaFFT(t *testing.T) {
	// nfft=2 should factor as [2, 1] which exercises the kfBfly2 m=1 path
	factors, ok := kfFactor(2)
	t.Logf("nfft=2: factors=%v, ok=%v", factors, ok)

	if !ok {
		t.Skip("nfft=2 not supported")
	}

	// If factors are [2, 1], then radix-2 with m=1 will be used
	if len(factors) >= 2 && factors[0] == 2 && factors[1] == 1 {
		t.Logf("kfBfly2M1 path will be exercised")
	}

	// Run FFT with size 2
	input := []complex64{complex(1.0, 2.0), complex(3.0, 4.0)}
	output := kissFFT32(input)

	// Expected: DFT of size 2
	// X[0] = x[0] + x[1] = (1+2i) + (3+4i) = (4+6i)
	// X[1] = x[0] - x[1] = (1+2i) - (3+4i) = (-2-2i)
	expected := []complex64{complex(4.0, 6.0), complex(-2.0, -2.0)}

	for i := range output {
		diff := output[i] - expected[i]
		if math.Abs(float64(real(diff))) > 1e-5 || math.Abs(float64(imag(diff))) > 1e-5 {
			t.Errorf("output[%d] = %v, expected %v", i, output[i], expected[i])
		}
	}
	t.Logf("FFT(2) passed: output=%v", output)
}

// TestKfBfly2M1Correctness verifies the optimized implementation matches reference.
func TestKfBfly2M1Correctness(t *testing.T) {
	t.Logf("kfBfly2M1Available: %v", kfBfly2M1Available())

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

			// Reference output
			refOut := make([]kissCpx, len(input))
			copy(refOut, input)
			kfBfly2M1Reference(refOut, n)

			// Optimized output
			optOut := make([]kissCpx, len(input))
			copy(optOut, input)
			// Verify copy worked
			for i := range input {
				if input[i] != optOut[i] {
					t.Logf("Copy mismatch at %d: input={%.6f,%.6f} optOut={%.6f,%.6f}",
						i, input[i].r, input[i].i, optOut[i].r, optOut[i].i)
				}
			}
			kfBfly2M1(optOut, n)

			// Debug: print first few values
			if n <= 4 {
				t.Logf("n=%d input:", n)
				for i := 0; i < len(input); i++ {
					t.Logf("  [%d] = {%.6f, %.6f}", i, input[i].r, input[i].i)
				}
				t.Logf("n=%d ref output:", n)
				for i := 0; i < len(refOut); i++ {
					t.Logf("  [%d] = {%.6f, %.6f}", i, refOut[i].r, refOut[i].i)
				}
				t.Logf("n=%d opt output:", n)
				for i := 0; i < len(optOut); i++ {
					t.Logf("  [%d] = {%.6f, %.6f}", i, optOut[i].r, optOut[i].i)
				}
			}

			// Compare
			var maxDiff float64
			var maxDiffIdx int
			for i := range refOut {
				dr := math.Abs(float64(refOut[i].r - optOut[i].r))
				di := math.Abs(float64(refOut[i].i - optOut[i].i))
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
				t.Errorf("n=%d: max diff = %.2e at index %d (ref: {%.6f, %.6f}, got: {%.6f, %.6f})",
					n, maxDiff, maxDiffIdx,
					refOut[maxDiffIdx].r, refOut[maxDiffIdx].i,
					optOut[maxDiffIdx].r, optOut[maxDiffIdx].i)
			}
		})
	}
}

// BenchmarkKfBfly2M1Portable benchmarks the kfBfly2M1 function on any platform
func BenchmarkKfBfly2M1Portable(b *testing.B) {
	sizes := []int{60, 120, 240, 480}
	for _, n := range sizes {
		input := make([]kissCpx, n*2)
		rng := rand.New(rand.NewSource(42))
		for i := range input {
			input[i] = kissCpx{r: float32(rng.Float64()), i: float32(rng.Float64())}
		}

		b.Run("Reference/n="+itoaPortable(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(data, input)
				kfBfly2M1Reference(data, n)
			}
		})

		b.Run("Optimized/n="+itoaPortable(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(data, input)
				kfBfly2M1(data, n)
			}
		})
	}
}

// BenchmarkKfBfly2M1PortableNoCopy benchmarks without copy overhead
func BenchmarkKfBfly2M1PortableNoCopy(b *testing.B) {
	sizes := []int{60, 120, 240, 480}
	for _, n := range sizes {
		input := make([]kissCpx, n*2)
		rng := rand.New(rand.NewSource(42))
		for i := range input {
			input[i] = kissCpx{r: float32(rng.Float64()), i: float32(rng.Float64())}
		}

		b.Run("Reference/n="+itoaPortable(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			copy(data, input)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				kfBfly2M1Reference(data, n)
			}
		})

		b.Run("Optimized/n="+itoaPortable(n), func(b *testing.B) {
			data := make([]kissCpx, len(input))
			copy(data, input)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				kfBfly2M1(data, n)
			}
		})
	}
}

func itoaPortable(n int) string {
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
