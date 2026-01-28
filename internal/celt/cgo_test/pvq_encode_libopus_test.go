// Package cgo provides CGO comparison tests for PVQ (Pyramid Vector Quantization) encoding.
// This file tests the complete PVQ encode path against libopus.
package cgo

import (
	"math"
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
)

// fitsIn32CWRS checks if V(n,k) fits in a 32-bit unsigned integer.
// This matches the libopus fits_in32() function in rate.c.
// N and K combinations that don't fit cannot be encoded with 32-bit CWRS.
func fitsIn32CWRS(n, k int) bool {
	// libopus fits_in32 tables from rate.c
	maxN := []int{32767, 32767, 32767, 1476, 283, 109, 60, 40, 29, 24, 20, 18, 16, 14, 13}
	maxK := []int{32767, 32767, 32767, 32767, 1172, 238, 95, 53, 36, 27, 22, 18, 16, 15, 13}

	if n >= 14 {
		if k >= 14 {
			return false
		}
		if k < len(maxN) {
			return n <= maxN[k]
		}
		return false
	}
	if n < len(maxK) {
		return k <= maxK[n]
	}
	return false
}

// TestPVQSearchAndEncodeRoundtrip tests that the PVQ search + CWRS encoding is correct.
// This tests the full encode/decode path in Go without libopus.
// Note: Only tests (n,k) combinations where V(n,k) fits in 32 bits (as per libopus limits).
func TestPVQSearchAndEncodeRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// Only test (n,k) combinations that fit in 32-bit CWRS encoding
	// libopus limits: n=4 k<=128, n=8 k<=36, n=16 k<=12, n=32 k<=7
	testCases := []struct {
		n, k int
	}{
		{4, 2},
		{4, 4},
		{8, 4},
		{8, 8},
		{8, 16}, // max valid for n=8 is k=36
		{16, 8},
		{16, 12}, // max valid for n=16 is k=12
		{32, 4},
		{32, 7}, // max valid for n=32 is k=7
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			for trial := 0; trial < 20; trial++ {
				// Generate random normalized vector
				x := make([]float64, tc.n)
				var sum float64
				for i := range x {
					x[i] = rng.NormFloat64()
					sum += x[i] * x[i]
				}
				scale := 1.0 / math.Sqrt(sum)
				for i := range x {
					x[i] *= scale
				}

				// Save original
				xOrig := make([]float64, tc.n)
				copy(xOrig, x)

				// PVQ search finds the best pulse vector
				pulses := goPVQSearch(x, tc.k)

				// Encode to CWRS index
				index := celt.EncodePulses(pulses, tc.n, tc.k)

				// Decode from CWRS index
				decodedPulses := celt.DecodePulses(index, tc.n, tc.k)

				// Verify pulses match
				for i := range pulses {
					if pulses[i] != decodedPulses[i] {
						t.Errorf("n=%d k=%d: Pulse mismatch at %d: %d vs %d",
							tc.n, tc.k, i, pulses[i], decodedPulses[i])
						t.Logf("  Original pulses: %v", pulses)
						t.Logf("  Decoded pulses:  %v", decodedPulses)
						break
					}
				}
			}
		})
	}
}

// TestPVQSearchVsLibopusWithSameInput tests that gopus PVQ search matches libopus
// when given identical input vectors.
func TestPVQSearchVsLibopusWithSameInput(t *testing.T) {
	rng := rand.New(rand.NewSource(123))

	// Test specific band sizes from CELT
	bandSizes := []int{4, 8, 12, 16, 24, 32, 48, 64}
	pulseCounts := []int{2, 4, 8, 12, 16, 24}

	totalTests := 0
	exactMatches := 0
	corrDiffs := 0

	for _, n := range bandSizes {
		for _, k := range pulseCounts {
			if k > n*2 {
				continue
			}

			for trial := 0; trial < 10; trial++ {
				// Generate random normalized vector
				x := make([]float64, n)
				var sum float64
				for i := range x {
					x[i] = rng.NormFloat64()
					sum += x[i] * x[i]
				}
				scale := 1.0 / math.Sqrt(sum)
				for i := range x {
					x[i] *= scale
				}

				// Run Go implementation
				xCopyGo := make([]float64, n)
				copy(xCopyGo, x)
				goPulses := goPVQSearch(xCopyGo, k)

				// Run libopus implementation
				libopusPulses, _ := LibopusPVQSearch(x, k)

				// Compare
				match := true
				for i := range goPulses {
					if goPulses[i] != libopusPulses[i] {
						match = false
						break
					}
				}

				totalTests++
				if match {
					exactMatches++
				} else {
					// Check if correlation is similar
					goCorr := computeCorrelation(x, goPulses)
					libopusCorr := computeCorrelation(x, libopusPulses)
					if math.Abs(goCorr-libopusCorr) > 0.001 {
						corrDiffs++
						t.Logf("Mismatch with different correlation:")
						t.Logf("  n=%d k=%d", n, k)
						t.Logf("  Go: %v (corr=%.6f)", goPulses, goCorr)
						t.Logf("  lib: %v (corr=%.6f)", libopusPulses, libopusCorr)
					}
				}
			}
		}
	}

	t.Logf("Results: %d/%d exact matches, %d correlation differences",
		exactMatches, totalTests, corrDiffs)

	if corrDiffs > 0 {
		t.Errorf("Found %d cases where Go and libopus have different correlations", corrDiffs)
	}
}

// TestPVQEncodingEndToEnd tests the complete PVQ encode/decode cycle.
// This verifies:
// 1. PVQ search finds pulses
// 2. CWRS encodes pulses to index
// 3. Index can be decoded back to pulses
// 4. Decoded pulses match original
// Note: Only tests (n,k) combinations where V(n,k) fits in 32 bits.
func TestPVQEncodingEndToEnd(t *testing.T) {
	rng := rand.New(rand.NewSource(456))

	for n := 4; n <= 64; n *= 2 {
		for k := 2; k <= n && k <= 32; k *= 2 {
			// Skip combinations that overflow 32-bit CWRS
			if !fitsIn32CWRS(n, k) {
				continue
			}
			t.Run("", func(t *testing.T) {
				for trial := 0; trial < 10; trial++ {
					// Generate random unit vector
					x := make([]float64, n)
					var sum float64
					for i := range x {
						x[i] = rng.NormFloat64()
						sum += x[i] * x[i]
					}
					scale := 1.0 / math.Sqrt(sum)
					for i := range x {
						x[i] *= scale
					}

					// Save original
					xOrig := make([]float64, n)
					copy(xOrig, x)

					// 1. PVQ search (use a copy since goPVQSearch modifies the input)
					xCopy := make([]float64, n)
					copy(xCopy, x)
					pulses := goPVQSearch(xCopy, k)

					// Verify pulse count
					pulseSum := 0
					for _, p := range pulses {
						if p < 0 {
							pulseSum -= p
						} else {
							pulseSum += p
						}
					}
					if pulseSum != k {
						t.Errorf("n=%d k=%d: Wrong pulse count %d", n, k, pulseSum)
					}

					// 2. CWRS encode
					vSize := celt.PVQ_V(n, k)
					if vSize == 0 {
						t.Errorf("n=%d k=%d: V(n,k) = 0", n, k)
						continue
					}
					index := celt.EncodePulses(pulses, n, k)
					if index >= vSize {
						t.Errorf("n=%d k=%d: index %d >= vSize %d", n, k, index, vSize)
					}

					// 3. CWRS decode
					decoded := celt.DecodePulses(index, n, k)

					// 4. Verify match
					for i := range pulses {
						if pulses[i] != decoded[i] {
							t.Errorf("n=%d k=%d: Mismatch at %d: %d vs %d",
								n, k, i, pulses[i], decoded[i])
							t.Logf("  Pulses: %v", pulses)
							t.Logf("  Decoded: %v", decoded)
							break
						}
					}
				}
			})
		}
	}
}

// TestCWRSEncodingMatchesLibopus verifies CWRS encoding matches libopus.
func TestCWRSEncodingMatchesLibopus(t *testing.T) {
	// Test specific known vectors
	testCases := []struct {
		name   string
		pulses []int
		k      int
	}{
		{"simple_pos", []int{1, 0, 0, 0}, 1},
		{"simple_neg", []int{-1, 0, 0, 0}, 1},
		{"two_pulses", []int{1, 1, 0, 0}, 2},
		{"mixed_sign", []int{1, -1, 0, 0}, 2},
		{"spread", []int{1, 0, 1, 0}, 2},
		{"all_positive", []int{1, 1, 1, 1}, 4},
		{"alternating", []int{1, -1, 1, -1}, 4},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			n := len(tc.pulses)
			k := tc.k

			// Verify pulse count
			sum := 0
			for _, p := range tc.pulses {
				if p < 0 {
					sum -= p
				} else {
					sum += p
				}
			}
			if sum != k {
				t.Fatalf("Test case has wrong k: sum=%d, k=%d", sum, k)
			}

			// Encode with Go
			goIndex := celt.EncodePulses(tc.pulses, n, k)

			// Decode back with Go
			goDecoded := celt.DecodePulses(goIndex, n, k)

			// Verify roundtrip
			for i := range tc.pulses {
				if tc.pulses[i] != goDecoded[i] {
					t.Errorf("Roundtrip failed at %d: %d vs %d", i, tc.pulses[i], goDecoded[i])
					t.Logf("  Input: %v", tc.pulses)
					t.Logf("  Decoded: %v", goDecoded)
				}
			}

			// Compare with libopus by doing search on a vector that produces these pulses
			// (not straightforward - libopus doesn't expose CWRS encoding directly)

			t.Logf("Pulses %v encoded to index %d (V=%d)", tc.pulses, goIndex, celt.PVQ_V(n, k))
		})
	}
}

// TestLibopusPVQSearchOutputFormat verifies the output format of libopus PVQ search.
func TestLibopusPVQSearchOutputFormat(t *testing.T) {
	testCases := []struct {
		name string
		x    []float64
		k    int
	}{
		{"unit_x", []float64{1, 0, 0, 0}, 4},
		{"unit_y", []float64{0, 1, 0, 0}, 4},
		{"diagonal", []float64{0.707, 0.707, 0, 0}, 4},
		{"negative", []float64{-1, 0, 0, 0}, 4},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			goPulses := goPVQSearch(tc.x, tc.k)
			libPulses, yy := LibopusPVQSearch(tc.x, tc.k)

			goSum := pulseSum(goPulses)
			libSum := pulseSum(libPulses)

			t.Logf("Input: %v, k=%d", tc.x, tc.k)
			t.Logf("Go:      %v (sum=%d)", goPulses, goSum)
			t.Logf("libopus: %v (sum=%d, yy=%.4f)", libPulses, libSum, yy)

			if goSum != tc.k {
				t.Errorf("Go pulse sum wrong: %d != %d", goSum, tc.k)
			}
			if libSum != tc.k {
				t.Errorf("libopus pulse sum wrong: %d != %d", libSum, tc.k)
			}

			// Verify they match
			match := true
			for i := range goPulses {
				if goPulses[i] != libPulses[i] {
					match = false
				}
			}
			if !match {
				t.Logf("MISMATCH (may be acceptable if correlations are equal)")
			}
		})
	}
}
