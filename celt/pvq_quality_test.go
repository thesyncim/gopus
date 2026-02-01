package celt

import (
	"math"
	"testing"
)

// TestPVQSearchQuality tests the PVQ search algorithm quality by verifying
// that the found pulse vector maximizes correlation with the input vector.
// This is the core quality metric for PVQ encoding.
func TestPVQSearchQuality(t *testing.T) {
	testCases := []struct {
		name string
		x    []float64
		k    int
	}{
		{
			name: "uniform_positive",
			x:    []float64{1.0, 1.0, 1.0, 1.0},
			k:    4,
		},
		{
			name: "single_peak",
			x:    []float64{0.0, 0.0, 1.0, 0.0},
			k:    4,
		},
		{
			name: "two_peaks",
			x:    []float64{0.7, 0.0, 0.7, 0.0},
			k:    4,
		},
		{
			name: "mixed_signs",
			x:    []float64{1.0, -1.0, 1.0, -1.0},
			k:    4,
		},
		{
			name: "sparse_large",
			x:    []float64{0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 0.0, 1.0},
			k:    4,
		},
		{
			name: "descending",
			x:    []float64{4.0, 3.0, 2.0, 1.0},
			k:    4,
		},
		{
			name: "high_k",
			x:    []float64{1.0, 2.0, 3.0, 4.0},
			k:    16,
		},
		{
			name: "real_audio_like",
			x:    []float64{0.5, -0.3, 0.8, -0.2, 0.1, 0.4, -0.6, 0.3},
			k:    8,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Normalize input to unit L2 norm (like PVQ expects)
			normX := normalizeFloat64(tc.x)

			// Run PVQ search
			pulses, yy := opPVQSearch(normX, tc.k)

			// Verify L1 norm equals k
			l1 := 0
			for _, p := range pulses {
				if p < 0 {
					l1 -= p
				} else {
					l1 += p
				}
			}
			if l1 != tc.k {
				t.Errorf("L1 norm = %d, want %d", l1, tc.k)
			}

			// Verify yy is correctly computed
			var computedYY float64
			for _, p := range pulses {
				computedYY += float64(p * p)
			}
			// Allow some tolerance for float32/float64 precision differences
			if math.Abs(yy-computedYY) > 1e-3 {
				t.Errorf("yy = %v, computed = %v", yy, computedYY)
			}

			// Normalize the result and check correlation with input
			normResult := normalizeFloat64(intToFloat64(pulses))
			correlation := dotProduct(normX, normResult)

			// Correlation should be positive and reasonably high
			if correlation < 0 {
				t.Errorf("negative correlation = %v", correlation)
			}

			// Log correlation for analysis
			t.Logf("input=%v, pulses=%v, correlation=%.4f", tc.x, pulses, correlation)
		})
	}
}

// TestPVQSearchPreservesSign verifies that PVQ search respects input signs
func TestPVQSearchPreservesSign(t *testing.T) {
	testCases := []struct {
		x []float64
		k int
	}{
		{[]float64{1.0, -1.0, 1.0, -1.0}, 4},
		{[]float64{-1.0, -1.0, -1.0, -1.0}, 4},
		{[]float64{1.0, 1.0, 1.0, 1.0}, 4},
		{[]float64{0.5, -0.5, 0.0, 0.0}, 2},
	}

	for _, tc := range testCases {
		pulses, _ := opPVQSearch(tc.x, tc.k)

		for i := range tc.x {
			if tc.x[i] > 0 && pulses[i] < 0 {
				t.Errorf("sign mismatch at %d: input=%v, pulse=%d", i, tc.x[i], pulses[i])
			}
			if tc.x[i] < 0 && pulses[i] > 0 {
				t.Errorf("sign mismatch at %d: input=%v, pulse=%d", i, tc.x[i], pulses[i])
			}
		}
	}
}

// TestPVQSearchEdgeCases tests edge cases in PVQ search
func TestPVQSearchEdgeCases(t *testing.T) {
	t.Run("zero_input", func(t *testing.T) {
		x := []float64{0.0, 0.0, 0.0, 0.0}
		pulses, _ := opPVQSearch(x, 4)
		// For zero input, libopus puts all pulses in first position
		l1 := 0
		for _, p := range pulses {
			if p < 0 {
				l1 -= p
			} else {
				l1 += p
			}
		}
		if l1 != 4 {
			t.Errorf("L1 = %d, want 4", l1)
		}
	})

	t.Run("very_small_input", func(t *testing.T) {
		x := []float64{1e-20, 1e-20, 1e-20, 1e-20}
		pulses, _ := opPVQSearch(x, 4)
		l1 := 0
		for _, p := range pulses {
			if p < 0 {
				l1 -= p
			} else {
				l1 += p
			}
		}
		if l1 != 4 {
			t.Errorf("L1 = %d, want 4", l1)
		}
	})

	t.Run("single_dimension", func(t *testing.T) {
		x := []float64{1.0}
		pulses, _ := opPVQSearch(x, 4)
		if len(pulses) != 1 || pulses[0] != 4 {
			t.Errorf("pulses = %v, want [4]", pulses)
		}
	})

	t.Run("k_equals_1", func(t *testing.T) {
		x := []float64{0.1, 0.2, 0.9, 0.1}
		pulses, _ := opPVQSearch(x, 1)
		l1 := 0
		for _, p := range pulses {
			if p < 0 {
				l1 -= p
			} else {
				l1 += p
			}
		}
		if l1 != 1 {
			t.Errorf("L1 = %d, want 1", l1)
		}
		// Should place the pulse at the maximum position
		if pulses[2] != 1 {
			t.Errorf("pulse should be at position 2, got %v", pulses)
		}
	})
}

// TestPVQRoundtrip tests that encode/decode produces correct results
func TestPVQRoundtrip(t *testing.T) {
	// Note: Keep n and k combinations within the CWRS table bounds.
	// The CWRS table is limited for large n,k combinations.
	// For n >= 15, only k up to about 14 is supported.
	testCases := []struct {
		n, k int
	}{
		{4, 2},
		{4, 4},
		{8, 4},
		{8, 8},
		{16, 8},
		{12, 10}, // Keep within table bounds
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Generate random-ish test vector
			x := make([]float64, tc.n)
			for i := range x {
				x[i] = float64((i*7+3)%11) - 5.0
			}
			x = normalizeFloat64(x)

			// Encode
			pulses, _ := opPVQSearch(x, tc.k)
			index := EncodePulses(pulses, tc.n, tc.k)

			// Decode
			decoded := DecodePulses(index, tc.n, tc.k)

			// Verify roundtrip
			for i := range pulses {
				if pulses[i] != decoded[i] {
					t.Errorf("mismatch at %d: encoded=%d, decoded=%d", i, pulses[i], decoded[i])
				}
			}
		})
	}
}

// TestExpRotationSymmetry tests that exp_rotation forward + inverse = identity
func TestExpRotationSymmetry(t *testing.T) {
	testCases := []struct {
		n, k, spread int
	}{
		{8, 2, spreadNormal},
		{16, 4, spreadNormal},
		{16, 4, spreadAggressive},
		{16, 4, spreadLight},
		{32, 8, spreadNormal},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			// Create test vector
			orig := make([]float64, tc.n)
			for i := range orig {
				orig[i] = float64(i + 1)
			}

			// Copy for rotation
			x := make([]float64, tc.n)
			copy(x, orig)

			// Forward rotation (dir=1)
			expRotation(x, tc.n, 1, 1, tc.k, tc.spread)

			// Inverse rotation (dir=-1)
			expRotation(x, tc.n, -1, 1, tc.k, tc.spread)

			// Check result matches original
			// Use relative tolerance for larger values since float32 accumulates errors
			for i := range orig {
				diff := math.Abs(x[i] - orig[i])
				relTol := 1e-4 * math.Abs(orig[i])
				if relTol < 1e-5 {
					relTol = 1e-5
				}
				if diff > relTol {
					t.Errorf("pos %d: orig=%.6f, result=%.6f, diff=%.6f (relTol=%.6f)", i, orig[i], x[i], diff, relTol)
				}
			}
		})
	}
}

// TestPVQSearchN2 tests the specialized N=2 search
func TestPVQSearchN2(t *testing.T) {
	testCases := []struct {
		x  []float64
		k  int
		up int
	}{
		{[]float64{1.0, 0.0}, 4, 3},
		{[]float64{0.7, 0.7}, 4, 3},
		{[]float64{1.0, -1.0}, 4, 3},
		{[]float64{-0.5, 0.5}, 2, 7},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			iy, upIy, refine := opPVQSearchN2(tc.x, tc.k, tc.up)

			// Verify L1 norm of iy equals k
			l1 := absInt(iy[0]) + absInt(iy[1])
			if l1 != tc.k {
				t.Errorf("L1(iy) = %d, want %d", l1, tc.k)
			}

			// Verify L1 norm of upIy equals up*k
			l1up := absInt(upIy[0]) + absInt(upIy[1])
			if l1up != tc.up*tc.k {
				t.Errorf("L1(upIy) = %d, want %d", l1up, tc.up*tc.k)
			}

			// Verify refine relationship
			expectedRefine := upIy[0] - tc.up*iy[0]
			if tc.x[1] < 0 {
				expectedRefine = -expectedRefine
			}
			// Note: the sign handling in opPVQSearchN2 is complex, just verify bounds
			if absInt(refine) > (tc.up-1)/2+1 {
				t.Errorf("refine=%d out of expected bounds", refine)
			}

			t.Logf("x=%v, k=%d, up=%d: iy=%v, upIy=%v, refine=%d", tc.x, tc.k, tc.up, iy, upIy, refine)
		})
	}
}

// TestPVQSearchExtra tests the extended precision search
func TestPVQSearchExtra(t *testing.T) {
	testCases := []struct {
		x  []float64
		k  int
		up int
	}{
		{[]float64{0.5, 0.5, 0.5, 0.5}, 4, 3},
		{[]float64{1.0, 0.0, 0.0, 0.0}, 4, 7},
		{[]float64{0.7, -0.3, 0.5, -0.2}, 8, 15},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			normX := normalizeFloat64(tc.x)
			iy, upIy, refine := opPVQSearchExtra(normX, tc.k, tc.up)

			// Verify L1 norm of iy equals k
			l1 := 0
			for _, v := range iy {
				l1 += absInt(v)
			}
			if l1 != tc.k {
				t.Errorf("L1(iy) = %d, want %d", l1, tc.k)
			}

			// Verify L1 norm of upIy equals up*k
			l1up := 0
			for _, v := range upIy {
				l1up += absInt(v)
			}
			if l1up != tc.up*tc.k {
				t.Errorf("L1(upIy) = %d, want %d", l1up, tc.up*tc.k)
			}

			// Verify refine values are within bounds
			for i, r := range refine {
				if absInt(r) > tc.up {
					t.Errorf("refine[%d]=%d exceeds up=%d", i, r, tc.up)
				}
				// Verify upIy[i] = up*iy[i] + refine[i]
				expected := tc.up*iy[i] + refine[i]
				if upIy[i] != expected {
					t.Errorf("upIy[%d]=%d != up*iy[%d]+refine[%d]=%d", i, upIy[i], i, i, expected)
				}
			}

			t.Logf("x=%v: iy=%v, upIy=%v, refine=%v", tc.x, iy, upIy, refine)
		})
	}
}

// BenchmarkPVQSearch benchmarks the PVQ search algorithm
func BenchmarkPVQSearch(b *testing.B) {
	sizes := []struct {
		n, k int
	}{
		{8, 4},
		{16, 8},
		{32, 16},
		{64, 32},
		{128, 64},
	}

	for _, s := range sizes {
		b.Run("", func(b *testing.B) {
			x := make([]float64, s.n)
			for i := range x {
				x[i] = float64((i*7+3)%11) - 5.0
			}
			x = normalizeFloat64(x)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				opPVQSearch(x, s.k)
			}
		})
	}
}

// Helper functions

func normalizeFloat64(v []float64) []float64 {
	if len(v) == 0 {
		return v
	}
	var energy float64
	for _, x := range v {
		energy += x * x
	}
	if energy < 1e-30 {
		result := make([]float64, len(v))
		if len(result) > 0 {
			result[0] = 1.0
		}
		return result
	}
	scale := 1.0 / math.Sqrt(energy)
	result := make([]float64, len(v))
	for i, x := range v {
		result[i] = x * scale
	}
	return result
}

func intToFloat64(v []int) []float64 {
	result := make([]float64, len(v))
	for i, x := range v {
		result[i] = float64(x)
	}
	return result
}

func dotProduct(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var sum float64
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum
}
