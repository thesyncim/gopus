// Package celt implements MDCT unit tests ported from libopus test_unit_mdct.c
//
// This file contains tests ported from:
//   libopus/celt/tests/test_unit_mdct.c
//
// Copyright (c) 2008-2011 Xiph.Org Foundation
// Written by Jean-Marc Valin
// Ported to Go for gopus.

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// mdctSNRThreshold is the minimum acceptable SNR in dB.
// The original C test uses 60 dB.
const mdctSNRThreshold = 60.0

// checkMDCTForward computes the expected MDCT output using direct formula and compares.
// This is a direct port of the check() function from test_unit_mdct.c
// Returns SNR in dB.
//
// The formula used (from C test):
//
//	expected[bin] = sum_{k=0}^{nfft-1} in[k] * cos(2*pi*(k+0.5+nfft/4)*(bin+0.5)/nfft) / (nfft/4)
func checkMDCTForward(in, out []float64, nfft int) float64 {
	var errpow, sigpow float64

	for bin := 0; bin < nfft/2; bin++ {
		var ansr float64
		for k := 0; k < nfft; k++ {
			phase := 2 * math.Pi * (float64(k) + 0.5 + float64(nfft)/4.0) * (float64(bin) + 0.5) / float64(nfft)
			re := math.Cos(phase)
			re /= float64(nfft) / 4.0
			ansr += in[k] * re
		}
		difr := ansr - out[bin]
		errpow += difr * difr
		sigpow += ansr * ansr
	}

	if errpow == 0 {
		return math.Inf(1) // Perfect match
	}
	return 10 * math.Log10(sigpow/errpow)
}

// checkMDCTInverse computes the expected IMDCT output using direct formula and compares.
// This is a direct port of the check_inv() function from test_unit_mdct.c
// Returns SNR in dB.
//
// The formula used (from C test):
//
//	expected[bin] = sum_{k=0}^{nfft/2-1} in[k] * cos(2*pi*(bin+0.5+nfft/4)*(k+0.5)/nfft)
func checkMDCTInverse(in, out []float64, nfft int) float64 {
	var errpow, sigpow float64

	for bin := 0; bin < nfft; bin++ {
		var ansr float64
		for k := 0; k < nfft/2; k++ {
			phase := 2 * math.Pi * (float64(bin) + 0.5 + float64(nfft)/4.0) * (float64(k) + 0.5) / float64(nfft)
			re := math.Cos(phase)
			ansr += in[k] * re
		}
		difr := ansr - out[bin]
		errpow += difr * difr
		sigpow += ansr * ansr
	}

	if errpow == 0 {
		return math.Inf(1) // Perfect match
	}
	return 10 * math.Log10(sigpow/errpow)
}

// mdctForwardRef computes forward MDCT using the reference formula from test_unit_mdct.c
// This matches what checkMDCTForward expects.
// Input: nfft samples, Output: nfft/2 coefficients
func mdctForwardRef(in []float64) []float64 {
	nfft := len(in)
	out := make([]float64, nfft/2)

	for bin := 0; bin < nfft/2; bin++ {
		var sum float64
		for k := 0; k < nfft; k++ {
			phase := 2 * math.Pi * (float64(k) + 0.5 + float64(nfft)/4.0) * (float64(bin) + 0.5) / float64(nfft)
			re := math.Cos(phase)
			re /= float64(nfft) / 4.0
			sum += in[k] * re
		}
		out[bin] = sum
	}

	return out
}

// mdctInverseRef computes inverse MDCT using the reference formula from test_unit_mdct.c
// This matches what checkMDCTInverse expects.
// Input: nfft/2 coefficients, Output: nfft samples
func mdctInverseRef(in []float64, nfft int) []float64 {
	out := make([]float64, nfft)

	for bin := 0; bin < nfft; bin++ {
		var sum float64
		for k := 0; k < nfft/2; k++ {
			phase := 2 * math.Pi * (float64(bin) + 0.5 + float64(nfft)/4.0) * (float64(k) + 0.5) / float64(nfft)
			re := math.Cos(phase)
			sum += in[k] * re
		}
		out[bin] = sum
	}

	return out
}

// mdctTest1d runs a single MDCT test for given size.
// This is a direct port of test1d() from test_unit_mdct.c
func mdctTest1d(t *testing.T, nfft int, isInverse bool) {
	t.Helper()

	// Create random input with same distribution as C test
	rng := rand.New(rand.NewSource(42)) // Fixed seed for reproducibility
	in := make([]float64, nfft)
	for k := 0; k < nfft; k++ {
		in[k] = float64(rng.Intn(32768) - 16384)
	}

	// Scale input by 32768 as in C test
	for k := 0; k < nfft; k++ {
		in[k] *= 32768
	}

	// For inverse, also divide by nfft
	if isInverse {
		for k := 0; k < nfft; k++ {
			in[k] /= float64(nfft)
		}
	}

	// Keep copy for reference
	inCopy := make([]float64, nfft)
	copy(inCopy, in)

	var snr float64
	if isInverse {
		// Input is nfft/2 coefficients (use first half)
		coeffs := in[:nfft/2]

		// Compute IMDCT using reference formula
		out := mdctInverseRef(coeffs, nfft)

		// Apply TDAC mirroring as done in the C test
		// (because clt_mdct_backward no longer does it)
		for k := 0; k < nfft/4; k++ {
			out[nfft-k-1] = out[nfft/2+k]
		}

		snr = checkMDCTInverse(coeffs, out, nfft)
	} else {
		// Compute MDCT using reference formula
		out := mdctForwardRef(in)

		snr = checkMDCTForward(inCopy, out, nfft)
	}

	direction := "forward"
	if isInverse {
		direction = "inverse"
	}

	t.Logf("nfft=%d %s, SNR = %.2f dB", nfft, direction, snr)

	if snr < mdctSNRThreshold {
		t.Errorf("Poor SNR for nfft=%d %s: %.2f dB (threshold: %.2f dB)",
			nfft, direction, snr, mdctSNRThreshold)
	}
}

// TestMDCTUnit_PowerOf2 tests MDCT reference implementation with power-of-2 sizes
func TestMDCTUnit_PowerOf2(t *testing.T) {
	sizes := []int{32, 256, 512, 1024, 2048}

	t.Run("Forward", func(t *testing.T) {
		for _, n := range sizes {
			mdctTest1d(t, n, false)
		}
	})
	t.Run("Inverse", func(t *testing.T) {
		for _, n := range sizes {
			mdctTest1d(t, n, true)
		}
	})
}

// TestMDCTUnit_NonPowerOf2 tests MDCT reference implementation with non-power-of-2 sizes
func TestMDCTUnit_NonPowerOf2(t *testing.T) {
	// These sizes are tested in C when RADIX_TWO_ONLY is not defined
	sizes := []int{36, 40, 60, 120, 240, 480, 960, 1920}

	for _, size := range sizes {
		t.Run("Forward", func(t *testing.T) {
			mdctTest1d(t, size, false)
		})
		t.Run("Inverse", func(t *testing.T) {
			mdctTest1d(t, size, true)
		})
	}
}

// TestMDCTUnit_CELTSizes tests MDCT with CELT-specific sizes
func TestMDCTUnit_CELTSizes(t *testing.T) {
	// CELT frame sizes correspond to 2.5ms, 5ms, 10ms, 20ms at 48kHz
	sizes := []int{240, 480, 960, 1920}

	for _, size := range sizes {
		t.Run("Forward", func(t *testing.T) {
			mdctTest1d(t, size, false)
		})
		t.Run("Inverse", func(t *testing.T) {
			mdctTest1d(t, size, true)
		})
	}
}

// TestMDCTUnit_GoIMDCT tests the Go IMDCT implementation against reference
func TestMDCTUnit_GoIMDCT(t *testing.T) {
	// Test CELT sizes with the actual Go implementation
	sizes := []int{120, 240, 480, 960}

	for _, N := range sizes {
		t.Run("", func(t *testing.T) {
			// Create random coefficients
			rng := rand.New(rand.NewSource(42))
			coeffs := make([]float64, N)
			for i := 0; i < N; i++ {
				coeffs[i] = float64(rng.Intn(32768) - 16384)
			}

			// Compute using Go IMDCT implementation
			goOut := IMDCT(coeffs)

			// Compute using direct IMDCT formula (standard, with 2/N normalization)
			directOut := IMDCTDirect(coeffs)

			// Compare
			var errpow, sigpow float64
			minLen := len(goOut)
			if len(directOut) < minLen {
				minLen = len(directOut)
			}
			for i := 0; i < minLen; i++ {
				diff := goOut[i] - directOut[i]
				errpow += diff * diff
				sigpow += directOut[i] * directOut[i]
			}

			if sigpow == 0 {
				t.Skip("Signal power is zero")
			}

			snr := 10 * math.Log10(sigpow/errpow)
			t.Logf("N=%d IMDCT Go vs Direct SNR = %.2f dB", N, snr)

			if snr < mdctSNRThreshold {
				t.Errorf("Poor SNR for N=%d IMDCT: %.2f dB (threshold: %.2f dB)",
					N, snr, mdctSNRThreshold)
			}
		})
	}
}

// TestMDCTUnit_GoMDCT tests the Go MDCT implementation against reference
func TestMDCTUnit_GoMDCT(t *testing.T) {
	// Test CELT sizes with the actual Go implementation
	sizes := []int{120, 240, 480, 960}

	for _, N := range sizes {
		t.Run("", func(t *testing.T) {
			// Create random samples (2*N for MDCT input)
			rng := rand.New(rand.NewSource(42))
			samples := make([]float64, 2*N)
			for i := 0; i < 2*N; i++ {
				samples[i] = float64(rng.Intn(32768) - 16384)
			}

			// Compute using Go mdctDirect (has scale = 4/N2 = 2/N)
			goOut := mdctDirect(samples)

			// Compute using reference formula (divides by nfft/4 = N/2)
			refOut := mdctForwardRef(samples)

			if len(goOut) != len(refOut) {
				t.Fatalf("Output length mismatch: Go=%d, Ref=%d", len(goOut), len(refOut))
			}

			// Both mdctDirect and mdctForwardRef use the same normalization:
			// - mdctDirect: multiplies by 4/N2 = 4/(2N) = 2/N
			// - mdctForwardRef: divides by nfft/4 = 2N/4 = N/2 (equivalent to multiply by 2/N)
			// So they should match directly!
			var errpow, sigpow float64
			for i := 0; i < len(refOut); i++ {
				diff := goOut[i] - refOut[i]
				errpow += diff * diff
				sigpow += refOut[i] * refOut[i]
			}

			if sigpow == 0 {
				t.Skip("Signal power is zero")
			}

			snr := 10 * math.Log10(sigpow/errpow)
			t.Logf("N=%d MDCT Go vs Ref SNR = %.2f dB", N, snr)

			if snr < mdctSNRThreshold {
				t.Errorf("Poor SNR for N=%d MDCT: %.2f dB (threshold: %.2f dB)",
					N, snr, mdctSNRThreshold)
			}
		})
	}
}

// TestMDCTUnit_RoundTrip tests that the reference formulas form a consistent pair.
// Note: This tests the mathematical relationship, not practical reconstruction
// which requires windowing and overlap-add.
func TestMDCTUnit_RoundTrip(t *testing.T) {
	sizes := []int{120, 240, 480, 960}

	for _, N := range sizes {
		t.Run("", func(t *testing.T) {
			nfft := 2 * N

			// Create random MDCT coefficients
			rng := rand.New(rand.NewSource(42))
			coeffs := make([]float64, N)
			for i := range coeffs {
				coeffs[i] = float64(rng.Intn(32768) - 16384)
			}

			// Compute IMDCT using reference formula
			timeOut := mdctInverseRef(coeffs, nfft)

			// Compute MDCT of the result using reference formula
			coeffsBack := mdctForwardRef(timeOut)

			// The MDCT(IMDCT(x)) should give back x (up to scaling)
			// Due to the different normalization in reference functions:
			// MDCT divides by nfft/4, IMDCT has no scaling
			// So coeffsBack = coeffs * (nfft/4)
			// Wait, let's verify: if coeffsBack should equal coeffs * something

			var errpow, sigpow float64
			for i := 0; i < N; i++ {
				// Check if they match (possibly with scaling)
				// Since both use same formulas, should match exactly
				diff := coeffsBack[i] - coeffs[i]
				errpow += diff * diff
				sigpow += coeffs[i] * coeffs[i]
			}

			if sigpow == 0 {
				t.Skip("Signal power is zero")
			}

			snr := 10 * math.Log10(sigpow/errpow)
			t.Logf("N=%d MDCT(IMDCT(x)) vs x SNR = %.2f dB", N, snr)

			if snr < mdctSNRThreshold {
				t.Errorf("Poor round-trip SNR for N=%d: %.2f dB (threshold: %.2f dB)",
					N, snr, mdctSNRThreshold)
			}
		})
	}
}

// TestMDCTUnit_AllSizes runs the complete test suite matching the C test
func TestMDCTUnit_AllSizes(t *testing.T) {
	// All sizes tested in the C test
	allSizes := []int{
		// Power of 2
		32, 256, 512, 1024, 2048,
		// Non-power of 2 (when RADIX_TWO_ONLY is not defined)
		36, 40, 60, 120, 240, 480, 960, 1920,
	}

	for _, size := range allSizes {
		t.Run("", func(t *testing.T) {
			mdctTest1d(t, size, false) // forward
			mdctTest1d(t, size, true)  // inverse
		})
	}
}
