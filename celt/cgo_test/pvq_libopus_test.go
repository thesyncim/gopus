//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for PVQ (Pyramid Vector Quantization).
// This file tests the Go opPVQSearch against libopus op_pvq_search.
package cgo

import (
	"math"
	"math/rand"
	"testing"
)

// goPVQSearch is the Go implementation from pvq_search.go (copied here for testing).
func goPVQSearch(xIn []float64, k int) []int {
	n := len(xIn)
	iy := make([]int, n)
	if n == 0 || k <= 0 {
		return iy
	}

	// Make a copy since we modify x
	x := make([]float64, n)
	copy(x, xIn)

	signx := make([]int, n)
	y := make([]float64, n)

	// Remove sign and initialize.
	for j := 0; j < n; j++ {
		if x[j] < 0 {
			signx[j] = 1
			x[j] = -x[j]
		}
	}

	xy := 0.0
	yy := 0.0
	pulsesLeft := k

	// Pre-search by projecting on the pyramid for large K.
	if k > (n >> 1) {
		sum := 0.0
		for j := 0; j < n; j++ {
			sum += x[j]
		}

		// Guard against tiny/huge/invalid sums.
		if !(sum > 1e-15 && sum < 64.0) {
			x[0] = 1.0
			for j := 1; j < n; j++ {
				x[j] = 0.0
			}
			sum = 1.0
		}

		rcp := (float64(k) + 0.8) / sum
		for j := 0; j < n; j++ {
			iy[j] = int(math.Floor(rcp * x[j]))
			y[j] = float64(iy[j])
			yy += y[j] * y[j]
			xy += x[j] * y[j]
			y[j] *= 2
			pulsesLeft -= iy[j]
		}
	}

	if pulsesLeft > n+3 {
		tmp := float64(pulsesLeft)
		yy += tmp * tmp
		yy += tmp * y[0]
		iy[0] += pulsesLeft
		pulsesLeft = 0
	}

	for i := 0; i < pulsesLeft; i++ {
		bestID := 0
		yy += 1

		rxy := xy + x[0]
		ryy := yy + y[0]
		bestNum := rxy * rxy
		bestDen := ryy

		for j := 1; j < n; j++ {
			rxy = xy + x[j]
			ryy = yy + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
		}

		xy += x[bestID]
		yy += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}

	for j := 0; j < n; j++ {
		if signx[j] != 0 {
			iy[j] = -iy[j]
		}
	}

	return iy
}

// pulseSum computes the L1 norm (sum of absolute values) of pulses.
func pulseSum(pulses []int) int {
	sum := 0
	for _, p := range pulses {
		if p < 0 {
			sum -= p
		} else {
			sum += p
		}
	}
	return sum
}

// computeCorrelation computes the correlation between the original vector and
// the normalized pulse vector.
func computeCorrelation(x []float64, pulses []int) float64 {
	// Compute the normalized pulse vector
	var energy float64
	for _, p := range pulses {
		energy += float64(p * p)
	}
	if energy <= 0 {
		return 0
	}
	scale := 1.0 / math.Sqrt(energy)

	// Compute dot product with original (normalized)
	var xEnergy float64
	for _, v := range x {
		xEnergy += v * v
	}
	if xEnergy <= 0 {
		return 0
	}
	xScale := 1.0 / math.Sqrt(xEnergy)

	var corr float64
	for i := range x {
		corr += (x[i] * xScale) * (float64(pulses[i]) * scale)
	}
	return corr
}

// TestPVQSearchBasic tests basic PVQ search functionality.
func TestPVQSearchBasic(t *testing.T) {
	testCases := []struct {
		name string
		x    []float64
		k    int
	}{
		{"simple_2d_k1", []float64{1.0, 0.0}, 1},
		{"simple_2d_k2", []float64{1.0, 1.0}, 2},
		{"simple_4d_k2", []float64{1.0, 0.5, 0.2, 0.1}, 2},
		{"simple_4d_k4", []float64{1.0, 0.5, 0.2, 0.1}, 4},
		{"negative", []float64{-1.0, 0.5, -0.2, 0.1}, 3},
		{"uniform_8d_k4", []float64{1, 1, 1, 1, 1, 1, 1, 1}, 4},
		{"sparse_8d_k2", []float64{0, 0, 0, 1, 0, 0, 0, 0.5}, 2},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Run Go implementation
			goPulses := goPVQSearch(tc.x, tc.k)

			// Run libopus implementation
			libopusPulses, _ := LibopusPVQSearch(tc.x, tc.k)

			// Verify pulse count
			goSum := pulseSum(goPulses)
			libopusSum := pulseSum(libopusPulses)

			if goSum != tc.k {
				t.Errorf("Go pulse count = %d, want %d", goSum, tc.k)
			}
			if libopusSum != tc.k {
				t.Errorf("libopus pulse count = %d, want %d", libopusSum, tc.k)
			}

			// Compare results
			match := true
			for i := range goPulses {
				if goPulses[i] != libopusPulses[i] {
					match = false
					break
				}
			}

			if !match {
				// Compute correlations to see which is better
				goCorr := computeCorrelation(tc.x, goPulses)
				libopusCorr := computeCorrelation(tc.x, libopusPulses)

				t.Logf("Mismatch: Go=%v, libopus=%v", goPulses, libopusPulses)
				t.Logf("Correlations: Go=%.6f, libopus=%.6f", goCorr, libopusCorr)

				// Both should achieve similar correlation (within small tolerance)
				if math.Abs(goCorr-libopusCorr) > 0.01 {
					t.Errorf("Significant correlation difference: Go=%.6f, libopus=%.6f",
						goCorr, libopusCorr)
				}
			}
		})
	}
}

// TestPVQSearchRandom tests PVQ search with random vectors.
func TestPVQSearchRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	testCases := []struct {
		n, k int
	}{
		{4, 2},
		{4, 4},
		{8, 4},
		{8, 8},
		{16, 8},
		{16, 16},
		{32, 8},
		{32, 16},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			mismatches := 0
			totalTests := 100
			maxCorrDiff := 0.0

			for trial := 0; trial < totalTests; trial++ {
				// Generate random normalized vector
				x := make([]float64, tc.n)
				var sum float64
				for i := range x {
					x[i] = rng.Float64()*2 - 1 // [-1, 1]
					sum += x[i] * x[i]
				}
				scale := 1.0 / math.Sqrt(sum)
				for i := range x {
					x[i] *= scale
				}

				// Run both implementations
				goPulses := goPVQSearch(x, tc.k)
				libopusPulses, _ := LibopusPVQSearch(x, tc.k)

				// Check for match
				match := true
				for i := range goPulses {
					if goPulses[i] != libopusPulses[i] {
						match = false
						break
					}
				}

				if !match {
					mismatches++
					goCorr := computeCorrelation(x, goPulses)
					libopusCorr := computeCorrelation(x, libopusPulses)
					diff := math.Abs(goCorr - libopusCorr)
					if diff > maxCorrDiff {
						maxCorrDiff = diff
					}
				}
			}

			t.Logf("n=%d, k=%d: %d/%d mismatches, max corr diff=%.6f",
				tc.n, tc.k, mismatches, totalTests, maxCorrDiff)

			// Some mismatches are OK (greedy algorithm can have ties)
			// but correlation should be very similar
			if maxCorrDiff > 0.05 {
				t.Errorf("Max correlation difference too large: %.6f", maxCorrDiff)
			}
		})
	}
}

// TestPVQSearchNormalized tests with properly normalized vectors like CELT uses.
func TestPVQSearchNormalized(t *testing.T) {
	rng := rand.New(rand.NewSource(123))

	// Test with various band sizes used in CELT
	bandSizes := []int{8, 16, 32, 48, 64, 96}
	pulseCounts := []int{2, 4, 8, 16}

	for _, n := range bandSizes {
		for _, k := range pulseCounts {
			if k > n {
				continue
			}

			t.Run("", func(t *testing.T) {
				totalTests := 50
				significantDiffs := 0

				for trial := 0; trial < totalTests; trial++ {
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

					// Run both
					goPulses := goPVQSearch(x, k)
					libopusPulses, _ := LibopusPVQSearch(x, k)

					goCorr := computeCorrelation(x, goPulses)
					libopusCorr := computeCorrelation(x, libopusPulses)

					// libopus should match or be better (it's the reference)
					if goCorr < libopusCorr-0.001 {
						significantDiffs++
						if significantDiffs <= 3 {
							t.Logf("Go correlation lower: Go=%.6f, libopus=%.6f (diff=%.6f)",
								goCorr, libopusCorr, libopusCorr-goCorr)
							t.Logf("  Go pulses: %v", goPulses)
							t.Logf("  libopus:   %v", libopusPulses)
						}
					}
				}

				if significantDiffs > totalTests/10 {
					t.Errorf("Too many cases where Go has lower correlation: %d/%d",
						significantDiffs, totalTests)
				}
			})
		}
	}
}

// TestPVQSearchSpecificVectors tests with specific vectors that have caused issues.
func TestPVQSearchSpecificVectors(t *testing.T) {
	testCases := []struct {
		name string
		x    []float64
		k    int
	}{
		// Test with all energy in one component
		{"single_peak", []float64{1, 0, 0, 0, 0, 0, 0, 0}, 4},

		// Test with two equal peaks
		{"two_peaks", []float64{0.707, 0.707, 0, 0, 0, 0, 0, 0}, 4},

		// Test with very small values
		{"small_values", []float64{0.001, 0.002, 0.003, 0.004}, 2},

		// Test with alternating signs
		{"alternating", []float64{0.5, -0.5, 0.5, -0.5}, 4},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			goPulses := goPVQSearch(tc.x, tc.k)
			libopusPulses, _ := LibopusPVQSearch(tc.x, tc.k)

			t.Logf("Input: %v", tc.x)
			t.Logf("Go:      %v (sum=%d)", goPulses, pulseSum(goPulses))
			t.Logf("libopus: %v (sum=%d)", libopusPulses, pulseSum(libopusPulses))

			goCorr := computeCorrelation(tc.x, goPulses)
			libopusCorr := computeCorrelation(tc.x, libopusPulses)
			t.Logf("Correlations: Go=%.6f, libopus=%.6f", goCorr, libopusCorr)

			// Verify pulse counts
			if pulseSum(goPulses) != tc.k {
				t.Errorf("Go pulse count wrong: %d != %d", pulseSum(goPulses), tc.k)
			}
			if pulseSum(libopusPulses) != tc.k {
				t.Errorf("libopus pulse count wrong: %d != %d", pulseSum(libopusPulses), tc.k)
			}
		})
	}
}

// TestPVQSearchEdgeCases tests edge cases.
func TestPVQSearchEdgeCases(t *testing.T) {
	// Test with k > n (more pulses than dimensions)
	t.Run("k_gt_n", func(t *testing.T) {
		x := []float64{1, 0, 0, 0}
		k := 8

		goPulses := goPVQSearch(x, k)
		libopusPulses, _ := LibopusPVQSearch(x, k)

		t.Logf("Go:      %v (sum=%d)", goPulses, pulseSum(goPulses))
		t.Logf("libopus: %v (sum=%d)", libopusPulses, pulseSum(libopusPulses))

		if pulseSum(goPulses) != k {
			t.Errorf("Go pulse count: %d, want %d", pulseSum(goPulses), k)
		}
		if pulseSum(libopusPulses) != k {
			t.Errorf("libopus pulse count: %d, want %d", pulseSum(libopusPulses), k)
		}
	})

	// Test with very large k
	t.Run("large_k", func(t *testing.T) {
		n := 8
		k := 64
		x := make([]float64, n)
		for i := range x {
			x[i] = 1.0 / math.Sqrt(float64(n))
		}

		goPulses := goPVQSearch(x, k)
		libopusPulses, _ := LibopusPVQSearch(x, k)

		t.Logf("Go sum=%d, libopus sum=%d", pulseSum(goPulses), pulseSum(libopusPulses))

		if pulseSum(goPulses) != k {
			t.Errorf("Go pulse count: %d, want %d", pulseSum(goPulses), k)
		}
	})
}

// BenchmarkPVQSearchGo benchmarks the Go implementation.
func BenchmarkPVQSearchGo(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	n := 32
	k := 16
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = goPVQSearch(x, k)
	}
}

// BenchmarkPVQSearchLibopus benchmarks the libopus implementation.
func BenchmarkPVQSearchLibopus(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	n := 32
	k := 16
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = LibopusPVQSearch(x, k)
	}
}
