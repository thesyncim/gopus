package celt

import (
	"math"
	"math/rand"
	"testing"
)

// TestKissFFT_Factors verifies factor computation matches libopus pattern.
func TestKissFFT_Factors(t *testing.T) {
	// Test sizes used in CELT MDCT
	testCases := []struct {
		nfft    int
		wantOK  bool
		factors []int // expected factors [p0,m0, p1,m1, ...]
	}{
		// 480 = 4 * 4 * 30 = 4 * 4 * 5 * 6 = 4 * 4 * 5 * 3 * 2
		// Actually: 480 = 2^5 * 3 * 5 = 32 * 15
		// Kiss FFT factors: prefer radix-4, then 2, then 3, 5
		{nfft: 480, wantOK: true},
		{nfft: 240, wantOK: true},
		{nfft: 120, wantOK: true},
		{nfft: 60, wantOK: true},
		// Power of 2
		{nfft: 256, wantOK: true},
		{nfft: 128, wantOK: true},
		// Unsupported (has prime factor > 5)
		{nfft: 7, wantOK: false},
		{nfft: 11, wantOK: false},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			factors, ok := kfFactor(tc.nfft)
			if ok != tc.wantOK {
				t.Errorf("kfFactor(%d) ok=%v, want %v", tc.nfft, ok, tc.wantOK)
				return
			}
			if !ok {
				return
			}
			// Verify factors are valid: p*m should be previous m
			product := tc.nfft
			for i := 0; i < len(factors)/2; i++ {
				p := factors[2*i]
				m := factors[2*i+1]
				if p*m != product {
					t.Errorf("nfft=%d: factors[%d]: p=%d, m=%d, p*m=%d != %d",
						tc.nfft, i, p, m, p*m, product)
				}
				product = m
			}
			// Final m should be 1
			if len(factors) > 0 && factors[len(factors)-1] != 1 {
				t.Errorf("nfft=%d: final m=%d, want 1", tc.nfft, factors[len(factors)-1])
			}
			t.Logf("nfft=%d: factors=%v", tc.nfft, factors)
		})
	}
}

// TestKissFFT_BitReversal verifies bit-reversal table is valid permutation.
func TestKissFFT_BitReversal(t *testing.T) {
	testSizes := []int{60, 120, 240, 480}

	for _, nfft := range testSizes {
		t.Run("", func(t *testing.T) {
			st := getKissFFTState(nfft)
			if st == nil || len(st.bitrev) != nfft {
				t.Fatalf("Failed to get state for nfft=%d", nfft)
			}

			// Check bitrev is a valid permutation
			seen := make([]bool, nfft)
			for i, v := range st.bitrev {
				if v < 0 || v >= nfft {
					t.Errorf("nfft=%d: bitrev[%d]=%d out of range", nfft, i, v)
					continue
				}
				if seen[v] {
					t.Errorf("nfft=%d: duplicate in bitrev: %d", nfft, v)
				}
				seen[v] = true
			}

			// Count unique values
			count := 0
			for _, s := range seen {
				if s {
					count++
				}
			}
			if count != nfft {
				t.Errorf("nfft=%d: bitrev has %d unique values, want %d", nfft, count, nfft)
			}

			t.Logf("nfft=%d: bitrev is valid permutation (first 10: %v)", nfft, st.bitrev[:min(10, len(st.bitrev))])
		})
	}
}

// TestKissFFT_Twiddles verifies twiddle factors are correct.
func TestKissFFT_Twiddles(t *testing.T) {
	testSizes := []int{60, 120, 240, 480}

	for _, nfft := range testSizes {
		t.Run("", func(t *testing.T) {
			st := getKissFFTState(nfft)
			if st == nil || len(st.w) != nfft {
				t.Fatalf("Failed to get state for nfft=%d", nfft)
			}

			// Check twiddles match expected values
			const pi = 3.14159265358979323846264338327
			var maxDiff float64
			for i := 0; i < nfft; i++ {
				phase := (-2.0 * pi / float64(nfft)) * float64(i)
				expR := float32(math.Cos(phase))
				expI := float32(math.Sin(phase))
				diffR := math.Abs(float64(st.w[i].r - expR))
				diffI := math.Abs(float64(st.w[i].i - expI))
				if diffR > maxDiff {
					maxDiff = diffR
				}
				if diffI > maxDiff {
					maxDiff = diffI
				}
			}

			t.Logf("nfft=%d: max twiddle diff=%.2e", nfft, maxDiff)

			if maxDiff > 1e-6 {
				t.Errorf("nfft=%d: twiddle error too large: %.2e", nfft, maxDiff)
			}
		})
	}
}

// TestKissFFT32_Accuracy tests kissFFT32 output accuracy.
func TestKissFFT32_Accuracy(t *testing.T) {
	testSizes := []int{60, 120, 240, 480}

	for _, nfft := range testSizes {
		t.Run("", func(t *testing.T) {
			// Generate random input
			rng := rand.New(rand.NewSource(42))
			x := make([]complex64, nfft)
			for i := 0; i < nfft; i++ {
				x[i] = complex(float32(rng.Float64()*2-1), float32(rng.Float64()*2-1))
			}

			// Compute using kissFFT32
			out := kissFFT32(x)

			// Compute using direct DFT as reference
			ref := dft32Fallback(x)

			// Compare
			var errPow, sigPow float64
			var maxDiff float64
			for i := 0; i < nfft; i++ {
				diffR := float64(real(out[i]) - real(ref[i]))
				diffI := float64(imag(out[i]) - imag(ref[i]))
				diff := math.Abs(diffR) + math.Abs(diffI)
				if diff > maxDiff {
					maxDiff = diff
				}
				errPow += diffR*diffR + diffI*diffI
				sigPow += float64(real(ref[i]))*float64(real(ref[i])) +
					float64(imag(ref[i]))*float64(imag(ref[i]))
			}

			snr := float64(0)
			if errPow > 0 && sigPow > 0 {
				snr = 10 * math.Log10(sigPow/errPow)
			} else if errPow == 0 {
				snr = 200
			}

			t.Logf("nfft=%d: SNR=%.2f dB, maxDiff=%.2e", nfft, snr, maxDiff)

			// For float32, we expect around 50-60 dB SNR due to single precision
			if snr < 50 {
				t.Errorf("nfft=%d: poor SNR %.2f dB (expected >= 50 dB)", nfft, snr)
			}
		})
	}
}

// TestKissFFT32_VsDft32 compares kissFFT32 with the original dft32.
func TestKissFFT32_VsDft32(t *testing.T) {
	testSizes := []int{60, 120, 240, 480}

	for _, nfft := range testSizes {
		t.Run("", func(t *testing.T) {
			// Generate random input
			rng := rand.New(rand.NewSource(42))
			x := make([]complex64, nfft)
			for i := 0; i < nfft; i++ {
				x[i] = complex(float32(rng.Float64()*2-1), float32(rng.Float64()*2-1))
			}

			// Compute using kissFFT32
			kissOut := kissFFT32(x)

			// Compute using original dft32
			dftOut := dft32(x)

			// Compare
			var maxDiff float64
			for i := 0; i < nfft; i++ {
				diffR := math.Abs(float64(real(kissOut[i]) - real(dftOut[i])))
				diffI := math.Abs(float64(imag(kissOut[i]) - imag(dftOut[i])))
				if diffR > maxDiff {
					maxDiff = diffR
				}
				if diffI > maxDiff {
					maxDiff = diffI
				}
			}

			t.Logf("nfft=%d: max diff between kissFFT32 and dft32: %.2e", nfft, maxDiff)

			// Both should compute the same DFT, just different algorithms
			// Due to different order of operations, there can be float32 precision differences
			// For float32, a difference of ~1e-2 is acceptable for large FFT sizes
			if maxDiff > 1e-2 {
				t.Errorf("nfft=%d: kissFFT32 differs from dft32 by %.2e", nfft, maxDiff)
			}
		})
	}
}
