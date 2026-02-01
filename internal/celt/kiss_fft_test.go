package celt

import (
	"fmt"
	"math"
	"math/cmplx"
	"testing"
)

// TestKissFFT64_SupportedSizes verifies that all CELT sizes are supported.
func TestKissFFT64_SupportedSizes(t *testing.T) {
	// CELT FFT sizes are n/4 where n is the MDCT size
	// MDCT sizes: 120, 240, 480, 960 -> FFT sizes: 30, 60, 120, 240
	sizes := []int{30, 60, 120, 240}

	for _, n := range sizes {
		state := GetKissFFT64State(n)
		if state == nil {
			t.Errorf("GetKissFFT64State(%d) returned nil, size should be supported", n)
			continue
		}
		if state.nfft != n {
			t.Errorf("state.nfft = %d, want %d", state.nfft, n)
		}
	}
}

// TestKissFFT64_Factorization verifies correct factorization.
func TestKissFFT64_Factorization(t *testing.T) {
	tests := []struct {
		n int
	}{
		{30},  // 30 = 2 * 3 * 5
		{60},  // 60 = 4 * 3 * 5
		{120}, // 120 = 2 * 4 * 3 * 5
		{240}, // 240 = 4 * 4 * 3 * 5
		{480}, // 480 = 2 * 4 * 4 * 3 * 5
		{960}, // 960 = 4 * 4 * 4 * 3 * 5
	}

	for _, tt := range tests {
		state := GetKissFFT64State(tt.n)
		if state == nil {
			t.Errorf("GetKissFFT64State(%d) returned nil", tt.n)
			continue
		}

		// Verify the product of radixes equals n
		product := 1
		for i := 0; i < len(state.factors); i += 2 {
			product *= state.factors[i]
		}
		if product != tt.n {
			t.Errorf("n=%d: product of factors = %d, want %d", tt.n, product, tt.n)
		}
	}
}

// TestKissFFT64_MatchesDFT verifies FFT output matches direct DFT.
func TestKissFFT64_MatchesDFT(t *testing.T) {
	sizes := []int{30, 60, 120, 240}

	for _, n := range sizes {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			// Create test input
			x := make([]complex128, n)
			for i := 0; i < n; i++ {
				x[i] = complex(math.Sin(float64(i)*0.1), math.Cos(float64(i)*0.2))
			}

			// Compute using KissFFT64
			state := GetKissFFT64State(n)
			if state == nil {
				t.Fatalf("GetKissFFT64State(%d) returned nil", n)
			}
			fftOut := make([]complex128, n)
			kissFFT64Forward(fftOut, x, state)

			// Compute using direct DFT
			dftOut := make([]complex128, n)
			directDFT64(dftOut, x)

			// Compare
			maxErr := 0.0
			for i := 0; i < n; i++ {
				err := cmplx.Abs(fftOut[i] - dftOut[i])
				if err > maxErr {
					maxErr = err
				}
			}

			// Allow for floating point errors
			if maxErr > 1e-9 {
				t.Errorf("Max error = %e, want < 1e-9", maxErr)
			}
		})
	}
}

// TestKissIFFT64_MatchesIDFT verifies IFFT output matches direct IDFT.
func TestKissIFFT64_MatchesIDFT(t *testing.T) {
	sizes := []int{30, 60, 120, 240}

	for _, n := range sizes {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			// Create test input
			x := make([]complex128, n)
			for i := 0; i < n; i++ {
				x[i] = complex(math.Sin(float64(i)*0.1), math.Cos(float64(i)*0.2))
			}

			// Compute using KissIFFT64
			state := GetKissFFT64State(n)
			if state == nil {
				t.Fatalf("GetKissFFT64State(%d) returned nil", n)
			}
			ifftOut := make([]complex128, n)
			state.KissIFFT(x, ifftOut)

			// Compute using direct IDFT
			idftOut := directIDFT64(x)

			// Compare
			maxErr := 0.0
			for i := 0; i < n; i++ {
				err := cmplx.Abs(ifftOut[i] - idftOut[i])
				if err > maxErr {
					maxErr = err
				}
			}

			// Allow for floating point errors
			if maxErr > 1e-9 {
				t.Errorf("Max error = %e, want < 1e-9", maxErr)
			}
		})
	}
}

// TestKissFFT64_Roundtrip verifies FFT -> IFFT = identity (within scaling).
func TestKissFFT64_Roundtrip(t *testing.T) {
	sizes := []int{30, 60, 120, 240}

	for _, n := range sizes {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			// Create test input
			x := make([]complex128, n)
			for i := 0; i < n; i++ {
				x[i] = complex(math.Sin(float64(i)*0.1), math.Cos(float64(i)*0.2))
			}

			state := GetKissFFT64State(n)
			if state == nil {
				t.Fatalf("GetKissFFT64State(%d) returned nil", n)
			}

			// Forward FFT (without scaling)
			fftOut := make([]complex128, n)
			kissFFT64Forward(fftOut, x, state)

			// Inverse FFT (with 1/n scaling)
			ifftOut := make([]complex128, n)
			state.KissIFFT(fftOut, ifftOut)

			// Compare with original (accounting for 1/n from IFFT but not from FFT)
			// Since kissFFT64Forward doesn't scale and KissIFFT doesn't scale either,
			// we need to manually scale by 1/n
			maxErr := 0.0
			scale := 1.0 / float64(n)
			for i := 0; i < n; i++ {
				expected := x[i]
				got := ifftOut[i] * complex(scale, 0)
				err := cmplx.Abs(got - expected)
				if err > maxErr {
					maxErr = err
				}
			}

			if maxErr > 1e-9 {
				t.Errorf("Max roundtrip error = %e, want < 1e-9", maxErr)
			}
		})
	}
}

// TestDftTo_UsesEfficientFFT verifies dftTo uses efficient FFT for supported sizes.
func TestDftTo_UsesEfficientFFT(t *testing.T) {
	sizes := []int{30, 60, 120, 240}

	for _, n := range sizes {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			// Create test input
			x := make([]complex128, n)
			for i := 0; i < n; i++ {
				x[i] = complex(float64(i), float64(n-i))
			}

			// Compute using dftTo (which should now use efficient FFT)
			out := make([]complex128, n)
			dftTo(out, x)

			// Compute using direct DFT for reference
			ref := make([]complex128, n)
			directDFT64(ref, x)

			// Compare
			maxErr := 0.0
			for i := 0; i < n; i++ {
				err := cmplx.Abs(out[i] - ref[i])
				if err > maxErr {
					maxErr = err
				}
			}

			if maxErr > 1e-9 {
				t.Errorf("Max error = %e, want < 1e-9", maxErr)
			}
		})
	}
}

// TestDftTo_MatchesDirect_CELTSizes verifies dftTo matches direct DFT for CELT FFT sizes.
// CELT uses FFT of size n/2 for MDCT of size n.
// MDCT sizes 120, 240, 480, 960 -> FFT sizes 60, 120, 240, 480.
func TestDftTo_MatchesDirect_CELTSizes(t *testing.T) {
	sizes := []int{60, 120, 240, 480}

	for _, n := range sizes {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			// Create test input
			x := make([]complex128, n)
			for i := 0; i < n; i++ {
				x[i] = complex(math.Sin(float64(i)*0.1), math.Cos(float64(i)*0.2))
			}

			// Compute using dftTo (which uses efficient FFT)
			out := make([]complex128, n)
			dftTo(out, x)

			// Compute using direct DFT for reference
			ref := make([]complex128, n)
			directDFT64(ref, x)

			// Compare
			maxErr := 0.0
			for i := 0; i < n; i++ {
				err := cmplx.Abs(out[i] - ref[i])
				if err > maxErr {
					maxErr = err
				}
			}

			if maxErr > 1e-9 {
				t.Errorf("Max error = %e, want < 1e-9", maxErr)
			}
		})
	}
}

// directDFT64 computes DFT directly (O(n^2)) for testing.
func directDFT64(out []complex128, x []complex128) {
	n := len(x)
	twoPi := -2.0 * math.Pi / float64(n)
	for k := 0; k < n; k++ {
		angle := twoPi * float64(k)
		wStep := complex(math.Cos(angle), math.Sin(angle))
		w := complex(1.0, 0.0)
		var sum complex128
		for i := 0; i < n; i++ {
			sum += x[i] * w
			w *= wStep
		}
		out[k] = sum
	}
}

// directIDFT64 computes inverse DFT directly (O(n^2)) for testing.
func directIDFT64(x []complex128) []complex128 {
	n := len(x)
	result := make([]complex128, n)
	twoPi := 2.0 * math.Pi / float64(n)
	scale := 1.0 / float64(n)

	for k := 0; k < n; k++ {
		angle := twoPi * float64(k)
		wStep := complex(math.Cos(angle), math.Sin(angle))
		w := complex(1.0, 0.0)
		var sum complex128
		for i := 0; i < n; i++ {
			sum += x[i] * w
			w *= wStep
		}
		result[k] = sum * complex(scale, 0)
	}
	return result
}

// BenchmarkDFT64_Direct benchmarks direct O(n^2) DFT.
func BenchmarkDFT64_Direct(b *testing.B) {
	n := 120
	x := make([]complex128, n)
	out := make([]complex128, n)
	for i := 0; i < n; i++ {
		x[i] = complex(float64(i), 0)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		directDFT64(out, x)
	}
}

// BenchmarkKissFFT64 benchmarks the mixed-radix FFT.
func BenchmarkKissFFT64(b *testing.B) {
	n := 120
	x := make([]complex128, n)
	out := make([]complex128, n)
	for i := 0; i < n; i++ {
		x[i] = complex(float64(i), 0)
	}
	state := GetKissFFT64State(n)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		kissFFT64Forward(out, x, state)
	}
}

// BenchmarkDftTo_Efficient benchmarks dftTo with efficient FFT.
func BenchmarkDftTo_Efficient(b *testing.B) {
	n := 120
	x := make([]complex128, n)
	out := make([]complex128, n)
	for i := 0; i < n; i++ {
		x[i] = complex(float64(i), 0)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dftTo(out, x)
	}
}

// BenchmarkIMDCT_CELTSizes benchmarks IMDCT for all CELT sizes.
func BenchmarkIMDCT_CELTSizes(b *testing.B) {
	sizes := []int{120, 240, 480, 960}

	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			spectrum := make([]float64, n)
			for i := 0; i < n; i++ {
				spectrum[i] = math.Sin(float64(i) * 0.05)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				IMDCT(spectrum)
			}
		})
	}
}

// BenchmarkIMDCTDirect benchmarks direct IMDCT (O(n^2)).
func BenchmarkIMDCTDirect(b *testing.B) {
	n := 120
	spectrum := make([]float64, n)
	for i := 0; i < n; i++ {
		spectrum[i] = math.Sin(float64(i) * 0.05)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		IMDCTDirect(spectrum)
	}
}
