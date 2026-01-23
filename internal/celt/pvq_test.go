package celt

import (
	"math"
	"testing"
)

// TestNormalizeVectorUnit verifies various input vectors normalize to unit L2 norm.
func TestNormalizeVectorUnit(t *testing.T) {
	tests := []struct {
		name   string
		input  []float64
		desc   string
	}{
		{
			name:  "3-4-5_triangle",
			input: []float64{3, 4},
			desc:  "Classic Pythagorean triple",
		},
		{
			name:  "already_unit",
			input: []float64{1, 0, 0},
			desc:  "Already unit length",
		},
		{
			name:  "negative_values",
			input: []float64{-3, 4, 0},
			desc:  "Mixed signs",
		},
		{
			name:  "single_element",
			input: []float64{7},
			desc:  "Single element normalizes to +/-1",
		},
		{
			name:  "single_negative",
			input: []float64{-7},
			desc:  "Single negative normalizes to -1",
		},
		{
			name:  "large_values",
			input: []float64{1000, 2000, 3000, 4000},
			desc:  "Large values should normalize without overflow",
		},
		{
			name:  "small_values",
			input: []float64{0.001, 0.002, 0.003},
			desc:  "Small values should normalize without precision loss",
		},
		{
			name:  "uniform_8",
			input: []float64{1, 1, 1, 1, 1, 1, 1, 1},
			desc:  "Uniform distribution",
		},
		{
			name:  "alternating_signs",
			input: []float64{1, -2, 3, -4, 5},
			desc:  "Alternating signs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeVector(tt.input)

			if len(result) != len(tt.input) {
				t.Errorf("NormalizeVector length = %d, want %d", len(result), len(tt.input))
				return
			}

			// Compute L2 norm
			var norm2 float64
			for _, x := range result {
				norm2 += x * x
			}

			// Verify unit norm (within tolerance)
			if math.Abs(norm2-1.0) > 1e-9 {
				t.Errorf("NormalizeVector(%v) has L2 norm^2 = %v, want 1.0", tt.input, norm2)
			}
		})
	}
}

// TestNormalizeVectorZero verifies zero vector handling.
func TestNormalizeVectorZero(t *testing.T) {
	tests := []struct {
		name  string
		input []float64
	}{
		{"zero_2", []float64{0, 0}},
		{"zero_3", []float64{0, 0, 0}},
		{"zero_8", []float64{0, 0, 0, 0, 0, 0, 0, 0}},
		{"near_zero", []float64{1e-20, 1e-20, 1e-20}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeVector(tt.input)

			// Should return input unchanged (no NaN or Inf)
			if len(result) != len(tt.input) {
				t.Errorf("NormalizeVector length = %d, want %d", len(result), len(tt.input))
				return
			}

			for i, x := range result {
				if math.IsNaN(x) || math.IsInf(x, 0) {
					t.Errorf("NormalizeVector[%d] = %v (NaN/Inf)", i, x)
				}
			}
		})
	}
}

// TestNormalizeVectorPreservesDirection verifies normalization preserves vector direction.
func TestNormalizeVectorPreservesDirection(t *testing.T) {
	input := []float64{3, 4}
	result := NormalizeVector(input)

	// Ratio of components should be preserved
	expectedRatio := input[0] / input[1]
	actualRatio := result[0] / result[1]

	if math.Abs(expectedRatio-actualRatio) > 1e-10 {
		t.Errorf("Direction not preserved: expected ratio %v, got %v", expectedRatio, actualRatio)
	}
}

// TestPVQUnitNorm verifies that PVQ decoded vectors always have unit L2 norm.
// This is critical for correct CELT band shape decoding.
func TestPVQUnitNorm(t *testing.T) {
	// Test various (n, k) combinations
	testCases := []struct {
		name string
		n, k int
	}{
		{"n4_k1", 4, 1},   // Small band, few pulses
		{"n8_k4", 8, 4},   // Typical band size
		{"n8_k8", 8, 8},   // More pulses than dimensions
		{"n16_k8", 16, 8}, // Larger band
		{"n2_k1", 2, 1},   // Minimal non-trivial case
		{"n3_k2", 3, 2},   // Small with multiple pulses
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			vCount := PVQ_V(tc.n, tc.k)

			// Test multiple indices within valid range
			testIndices := []uint32{0, 1}
			if vCount > 2 {
				testIndices = append(testIndices, vCount/2)
			}
			if vCount > 3 {
				testIndices = append(testIndices, vCount-1)
			}

			for _, idx := range testIndices {
				// Decode pulses
				pulses := DecodePulses(idx, tc.n, tc.k)
				if pulses == nil {
					t.Errorf("DecodePulses(%d, %d, %d) returned nil", idx, tc.n, tc.k)
					continue
				}

				// Convert to float and normalize
				floatPulses := intToFloat(pulses)
				normalized := NormalizeVector(floatPulses)

				// Verify unit L2 norm
				var norm2 float64
				for _, x := range normalized {
					norm2 += x * x
				}

				if math.Abs(norm2-1.0) > 1e-6 {
					t.Errorf("PVQ(n=%d, k=%d, idx=%d) L2 norm^2 = %v, want 1.0 (pulses=%v)",
						tc.n, tc.k, idx, norm2, pulses)
				}
			}
		})
	}
}

// TestPVQDeterminism verifies that same index, n, k always produces same output.
func TestPVQDeterminism(t *testing.T) {
	testCases := []struct {
		n, k  int
		index uint32
	}{
		{4, 2, 0},
		{4, 2, 5},
		{8, 4, 100},
		{8, 4, 1000},
		{16, 8, 50000},
	}

	for _, tc := range testCases {
		vCount := PVQ_V(tc.n, tc.k)
		if tc.index >= vCount {
			tc.index = vCount - 1
		}

		// Decode twice
		result1 := DecodePulses(tc.index, tc.n, tc.k)
		result2 := DecodePulses(tc.index, tc.n, tc.k)

		if result1 == nil || result2 == nil {
			t.Errorf("DecodePulses returned nil")
			continue
		}

		if len(result1) != len(result2) {
			t.Errorf("Length mismatch: %d vs %d", len(result1), len(result2))
			continue
		}

		for i := range result1 {
			if result1[i] != result2[i] {
				t.Errorf("DecodePulses(%d, %d, %d) not deterministic: %v vs %v",
					tc.index, tc.n, tc.k, result1, result2)
				break
			}
		}
	}
}

// TestPVQEnergyDistribution verifies that PVQ distributes energy across bins.
// This catches bugs where all pulses concentrate in a single position.
func TestPVQEnergyDistribution(t *testing.T) {
	n := 8
	k := 4
	vCount := PVQ_V(n, k)

	// Track how many times each position is non-zero across all codewords
	nonzeroCount := make([]int, n)
	totalNonzero := 0

	// Sample codewords (or all if small enough)
	sampleSize := int(vCount)
	if sampleSize > 1000 {
		sampleSize = 1000
	}

	for idx := uint32(0); idx < uint32(sampleSize); idx++ {
		pulses := DecodePulses(idx, n, k)
		if pulses == nil {
			continue
		}

		for i, p := range pulses {
			if p != 0 {
				nonzeroCount[i]++
				totalNonzero++
			}
		}
	}

	// Verify energy is not concentrated in just one or two positions
	// For (n=8, k=4), we expect reasonable distribution
	t.Logf("Nonzero distribution across %d samples: %v", sampleSize, nonzeroCount)

	zerosCount := 0
	for _, count := range nonzeroCount {
		if count == 0 {
			zerosCount++
		}
	}

	// At least half the positions should have some pulses
	if zerosCount > n/2 {
		t.Errorf("Too many positions with zero pulses: %d/%d positions never used", zerosCount, n)
	}
}

// TestPVQAllCodewordsHaveCorrectK verifies every codeword has exactly k pulses.
func TestPVQAllCodewordsHaveCorrectK(t *testing.T) {
	testCases := []struct {
		n, k int
	}{
		{2, 1},
		{2, 2},
		{3, 2},
		{4, 2},
		{4, 3},
		{5, 3},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			vCount := PVQ_V(tc.n, tc.k)

			for idx := uint32(0); idx < vCount; idx++ {
				pulses := DecodePulses(idx, tc.n, tc.k)
				if pulses == nil {
					t.Errorf("DecodePulses(%d, %d, %d) returned nil", idx, tc.n, tc.k)
					continue
				}

				// Sum of absolute values should equal k
				sum := 0
				for _, p := range pulses {
					if p < 0 {
						sum -= p
					} else {
						sum += p
					}
				}

				if sum != tc.k {
					t.Errorf("DecodePulses(%d, %d, %d) = %v has L1=%d, want %d",
						idx, tc.n, tc.k, pulses, sum, tc.k)
				}
			}
		})
	}
}

// TestPVQZeroPulses verifies k=0 returns zero vector.
func TestPVQZeroPulses(t *testing.T) {
	for n := 1; n <= 10; n++ {
		pulses := DecodePulses(0, n, 0)
		if pulses == nil {
			t.Errorf("DecodePulses(0, %d, 0) returned nil", n)
			continue
		}

		if len(pulses) != n {
			t.Errorf("DecodePulses(0, %d, 0) length = %d, want %d", n, len(pulses), n)
			continue
		}

		for i, p := range pulses {
			if p != 0 {
				t.Errorf("DecodePulses(0, %d, 0)[%d] = %d, want 0", n, i, p)
			}
		}
	}
}

// TestPVQCodebookSize verifies the codebook size V(n,k) matches expected values.
func TestPVQCodebookSize(t *testing.T) {
	// For small (n, k), we can verify all codewords are unique and count them
	testCases := []struct {
		n, k     int
		expected uint32
	}{
		{2, 1, 4},   // [+1,0], [-1,0], [0,+1], [0,-1]
		{2, 2, 8},   // 4 sign combinations * 2 distribution patterns
		{3, 1, 6},   // 3 positions * 2 signs
		{3, 2, 18},  // Computed from recurrence
		{4, 1, 8},   // 4 positions * 2 signs
		{4, 2, 32},  // Computed from recurrence
	}

	for _, tc := range testCases {
		v := PVQ_V(tc.n, tc.k)
		if v != tc.expected {
			t.Errorf("V(%d, %d) = %d, want %d", tc.n, tc.k, v, tc.expected)
		}

		// Verify by actually enumerating all unique codewords
		seen := make(map[string]bool)
		for idx := uint32(0); idx < v; idx++ {
			pulses := DecodePulses(idx, tc.n, tc.k)
			key := ""
			for _, p := range pulses {
				key += string(rune(p + 1000)) // Simple unique key
			}
			seen[key] = true
		}

		if uint32(len(seen)) != tc.expected {
			t.Errorf("Enumerated %d unique codewords for V(%d, %d), want %d",
				len(seen), tc.n, tc.k, tc.expected)
		}
	}
}

// BenchmarkNormalizeVectorSmall benchmarks normalization for small vectors.
func BenchmarkNormalizeVectorSmall(b *testing.B) {
	v := []float64{1, 2, 3, 4}
	for i := 0; i < b.N; i++ {
		_ = NormalizeVector(v)
	}
}

// BenchmarkNormalizeVectorLarge benchmarks normalization for large vectors.
func BenchmarkNormalizeVectorLarge(b *testing.B) {
	v := make([]float64, 256)
	for i := range v {
		v[i] = float64(i + 1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NormalizeVector(v)
	}
}

// BenchmarkIntToFloat benchmarks int to float conversion.
func BenchmarkIntToFloat(b *testing.B) {
	v := make([]int, 16)
	for i := range v {
		v[i] = i - 8
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = intToFloat(v)
	}
}
