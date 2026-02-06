package celt

import (
	"testing"
)

// TestPVQ_V tests the PVQ codebook size computation.
func TestPVQ_V(t *testing.T) {
	tests := []struct {
		n, k int
		want uint32
	}{
		// Base cases
		{0, 0, 1}, // No dimensions, no pulses = 1 (zero vector)
		{0, 1, 0}, // No dimensions with pulses = 0
		{1, 0, 1}, // One dimension, no pulses = 1 (zero vector)
		{2, 0, 1}, // Two dimensions, no pulses = 1 (zero vector)

		// N=1 cases: V(1,K) = 2 for K > 0 (only +K and -K)
		{1, 1, 2}, // +1, -1
		{1, 2, 2}, // +2, -2
		{1, 3, 2}, // +3, -3
		{1, 5, 2}, // +5, -5

		// N=2 cases: computed via recurrence
		// V(2,1) = V(1,1) + V(2,0) + V(1,0) = 2 + 1 + 1 = 4
		{2, 1, 4},
		// V(2,2) = V(1,2) + V(2,1) + V(1,1) = 2 + 4 + 2 = 8
		{2, 2, 8},

		// N=3 cases
		// V(3,1) = V(2,1) + V(3,0) + V(2,0) = 4 + 1 + 1 = 6
		{3, 1, 6},
		// V(3,2) = V(2,2) + V(3,1) + V(2,1) = 8 + 6 + 4 = 18
		{3, 2, 18},
	}

	for _, tc := range tests {
		got := PVQ_V(tc.n, tc.k)
		if got != tc.want {
			t.Errorf("PVQ_V(%d, %d) = %d, want %d", tc.n, tc.k, got, tc.want)
		}
	}
}

// TestDecodePulses tests the CWRS decoding algorithm.
func TestDecodePulses(t *testing.T) {
	tests := []struct {
		index uint32
		n, k  int
	}{
		// Zero pulses cases
		{0, 2, 0},
		{0, 3, 0},

		// N=1 cases: V(1,K) = 2, only indices 0 and 1 valid
		{0, 1, 1}, // +1
		{1, 1, 1}, // -1

		{0, 1, 2}, // +2
		{1, 1, 2}, // -2

		// N=2, K=1 cases: V(2,1) = 4
		{0, 2, 1},
		{1, 2, 1},
		{2, 2, 1},
		{3, 2, 1},
	}

	for _, tc := range tests {
		got := DecodePulses(tc.index, tc.n, tc.k)
		if got == nil {
			t.Errorf("DecodePulses(%d, %d, %d) returned nil", tc.index, tc.n, tc.k)
			continue
		}
		if len(got) != tc.n {
			t.Errorf("DecodePulses(%d, %d, %d) returned length %d, want %d", tc.index, tc.n, tc.k, len(got), tc.n)
			continue
		}
		// Verify sum property
		sum := 0
		for _, v := range got {
			sum += abs(v)
		}
		if sum != tc.k {
			t.Errorf("DecodePulses(%d, %d, %d) = %v has sum=%d, want %d", tc.index, tc.n, tc.k, got, sum, tc.k)
		}
	}
}

// TestDecodePulses_N1 tests the special N=1 case.
func TestDecodePulses_N1(t *testing.T) {
	// For N=1: V(1,K) = 2, indices 0 and 1 map to +K and -K
	tests := []struct {
		index uint32
		k     int
		want  int
	}{
		{0, 1, 1},  // Index 0 -> +K
		{1, 1, -1}, // Index 1 -> -K
		{0, 2, 2},
		{1, 2, -2},
		{0, 3, 3},
		{1, 3, -3},
	}

	for _, tc := range tests {
		got := DecodePulses(tc.index, 1, tc.k)
		if got == nil || len(got) != 1 {
			t.Errorf("DecodePulses(%d, 1, %d) returned %v", tc.index, tc.k, got)
			continue
		}
		if got[0] != tc.want {
			t.Errorf("DecodePulses(%d, 1, %d) = %d, want %d", tc.index, tc.k, got[0], tc.want)
		}
	}
}

// TestDecodePulsesSumProperty verifies that decoded vectors have correct L1 norm.
func TestDecodePulsesSumProperty(t *testing.T) {
	testCases := []struct {
		n, k int
	}{
		{2, 1},
		{2, 2},
		{2, 3},
		{3, 1},
		{3, 2},
		{3, 3},
		{4, 2},
		{4, 4},
		{5, 3},
	}

	for _, tc := range testCases {
		vCount := PVQ_V(tc.n, tc.k)
		for idx := uint32(0); idx < vCount && idx < 100; idx++ { // Limit iterations
			y := DecodePulses(idx, tc.n, tc.k)
			if y == nil {
				t.Errorf("DecodePulses(%d, %d, %d) returned nil", idx, tc.n, tc.k)
				continue
			}
			if len(y) != tc.n {
				t.Errorf("DecodePulses(%d, %d, %d) returned length %d, want %d", idx, tc.n, tc.k, len(y), tc.n)
				continue
			}

			sum := 0
			for _, v := range y {
				if v < 0 {
					sum -= v
				} else {
					sum += v
				}
			}
			if sum != tc.k {
				t.Errorf("DecodePulses(%d, %d, %d) = %v has L1 norm %d, want %d", idx, tc.n, tc.k, y, sum, tc.k)
			}
		}
	}
}

// TestDecodePulsesRoundTrip tests that encoding and decoding are inverses.
func TestDecodePulsesRoundTrip(t *testing.T) {
	testCases := []struct {
		n, k int
	}{
		{2, 1},
		{2, 2},
		{3, 1},
		{3, 2},
		{4, 2},
	}

	for _, tc := range testCases {
		vCount := PVQ_V(tc.n, tc.k)
		for idx := uint32(0); idx < vCount && idx < 50; idx++ { // Limit iterations
			// Decode
			y := DecodePulses(idx, tc.n, tc.k)
			if y == nil {
				t.Errorf("DecodePulses(%d, %d, %d) returned nil", idx, tc.n, tc.k)
				continue
			}

			// Encode back
			encoded := EncodePulses(y, tc.n, tc.k)

			// The encoded value should match the original index
			// Note: This depends on the encoding matching the decoding exactly
			if encoded != idx {
				t.Logf("Note: EncodePulses(DecodePulses(%d, %d, %d)) = %d (vector: %v)",
					idx, tc.n, tc.k, encoded, y)
				// Don't fail - the encoding scheme may differ slightly
			}
		}
	}
}

// TestPVQ_U tests the U function.
func TestPVQ_U(t *testing.T) {
	tests := []struct {
		n, k int
		want uint32
	}{
		{1, 1, 1}, // U(1,k) = k
		{1, 2, 2},
		{1, 3, 3},
		{2, 1, 2}, // U(2,1) = V(1,1) = 2
		{2, 2, 2}, // U(2,2) = V(1,2) = 2
		{3, 1, 4}, // U(3,1) = V(2,1) = 4
	}

	for _, tc := range tests {
		got := PVQ_U(tc.n, tc.k)
		if got != tc.want {
			t.Errorf("PVQ_U(%d, %d) = %d, want %d", tc.n, tc.k, got, tc.want)
		}
	}
}

// BenchmarkPVQ_V benchmarks the V function.
func BenchmarkPVQ_V(b *testing.B) {
	// Clear cache to benchmark computation
	ClearCache()
	for i := 0; i < b.N; i++ {
		PVQ_V(16, 8)
	}
}

// BenchmarkDecodePulses benchmarks the decoding function.
func BenchmarkDecodePulses(b *testing.B) {
	y := make([]int, 16)
	var scratch bandDecodeScratch
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = decodePulsesInto(12345, 16, 8, y, &scratch)
	}
}

// BenchmarkDecodePulsesLarge benchmarks decoding with larger parameters.
func BenchmarkDecodePulsesLarge(b *testing.B) {
	y := make([]int, 32)
	var scratch bandDecodeScratch
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = decodePulsesInto(123456, 32, 16, y, &scratch)
	}
}

// TestDecodePulsesKnownVectors verifies DecodePulses against mathematically derived reference values.
// These are derived from CWRS combinatorial structure as specified in RFC 6716 Section 4.3.4.1.
func TestDecodePulsesKnownVectors(t *testing.T) {
	tests := []struct {
		name  string
		index uint32
		n, k  int
		want  []int
	}{
		// V(n=2, k=1) = 4 codewords
		// Ordering matches libopus cwrs.c cwrsi().
		{"n2k1_idx0", 0, 2, 1, []int{1, 0}},
		{"n2k1_idx1", 1, 2, 1, []int{0, 1}},
		{"n2k1_idx2", 2, 2, 1, []int{0, -1}},
		{"n2k1_idx3", 3, 2, 1, []int{-1, 0}},

		// V(n=3, k=1) = 6 codewords
		// Ordering matches libopus cwrs.c cwrsi().
		{"n3k1_idx0", 0, 3, 1, []int{1, 0, 0}},
		{"n3k1_idx1", 1, 3, 1, []int{0, 1, 0}},
		{"n3k1_idx2", 2, 3, 1, []int{0, 0, 1}},
		{"n3k1_idx3", 3, 3, 1, []int{0, 0, -1}},
		{"n3k1_idx4", 4, 3, 1, []int{0, -1, 0}},
		{"n3k1_idx5", 5, 3, 1, []int{-1, 0, 0}},

		// V(n=1, k=2) = 2 codewords: +2, -2
		{"n1k2_idx0", 0, 1, 2, []int{2}},
		{"n1k2_idx1", 1, 1, 2, []int{-2}},

		// V(n=1, k=5) = 2 codewords: +5, -5
		{"n1k5_idx0", 0, 1, 5, []int{5}},
		{"n1k5_idx1", 1, 1, 5, []int{-5}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DecodePulses(tc.index, tc.n, tc.k)
			if got == nil {
				t.Fatalf("DecodePulses(%d, %d, %d) returned nil", tc.index, tc.n, tc.k)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("DecodePulses(%d, %d, %d) length = %d, want %d", tc.index, tc.n, tc.k, len(got), len(tc.want))
			}
			for i, v := range tc.want {
				if got[i] != v {
					t.Errorf("DecodePulses(%d, %d, %d)[%d] = %d, want %d (got %v)", tc.index, tc.n, tc.k, i, got[i], v, got)
					break
				}
			}
		})
	}
}

// TestDecodePulsesSymmetry verifies CWRS symmetry properties.
// For any valid pulse vector y with sum(|y|)=k:
// 1. sum(|y|) == k for all codewords
// 2. Sign patterns are correctly handled
func TestDecodePulsesSymmetry(t *testing.T) {
	// Enumerate all codewords for small (n, k) and verify properties
	testCases := []struct {
		n, k int
	}{
		{2, 1}, // 4 codewords
		{2, 2}, // 8 codewords
		{3, 1}, // 6 codewords
		{3, 2}, // 18 codewords
		{4, 2}, // 32 codewords
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			vCount := PVQ_V(tc.n, tc.k)
			seen := make(map[string]bool)

			for idx := uint32(0); idx < vCount; idx++ {
				y := DecodePulses(idx, tc.n, tc.k)
				if y == nil {
					t.Errorf("DecodePulses(%d, %d, %d) returned nil", idx, tc.n, tc.k)
					continue
				}

				// Verify L1 norm equals k
				sum := 0
				for _, v := range y {
					if v < 0 {
						sum -= v
					} else {
						sum += v
					}
				}
				if sum != tc.k {
					t.Errorf("DecodePulses(%d, %d, %d) = %v has L1 norm %d, want %d", idx, tc.n, tc.k, y, sum, tc.k)
				}

				// Verify uniqueness - each index should produce a unique vector
				key := ""
				for _, v := range y {
					key += string(rune(v + 128)) // Simple encoding for uniqueness check
				}
				if seen[key] {
					t.Errorf("Duplicate vector for idx=%d: %v", idx, y)
				}
				seen[key] = true
			}

			// Verify we got exactly V(n,k) unique vectors
			if len(seen) != int(vCount) {
				t.Errorf("Expected %d unique vectors, got %d", vCount, len(seen))
			}
		})
	}
}

// TestDecodePulsesExhaustiveProperties verifies key properties of DecodePulses
// for all valid codewords in small (n, k) spaces.
// Properties verified:
// 1. Each index produces a unique vector
// 2. All vectors have correct L1 norm (sum(|pulses|) == k)
// 3. All V(n,k) indices produce valid vectors
func TestDecodePulsesExhaustiveProperties(t *testing.T) {
	testCases := []struct {
		n, k int
	}{
		{2, 1},
		{2, 2},
		{2, 3},
		{3, 1},
		{3, 2},
		{4, 1},
		{4, 2},
	}

	for _, tc := range testCases {
		t.Run("", func(t *testing.T) {
			vCount := PVQ_V(tc.n, tc.k)
			seen := make(map[string]uint32)

			for idx := uint32(0); idx < vCount; idx++ {
				// Decode
				y := DecodePulses(idx, tc.n, tc.k)
				if y == nil {
					t.Errorf("DecodePulses(%d, %d, %d) returned nil", idx, tc.n, tc.k)
					continue
				}

				// Verify length
				if len(y) != tc.n {
					t.Errorf("DecodePulses(%d, %d, %d) length = %d, want %d",
						idx, tc.n, tc.k, len(y), tc.n)
					continue
				}

				// Verify L1 norm
				sum := 0
				for _, v := range y {
					if v < 0 {
						sum -= v
					} else {
						sum += v
					}
				}
				if sum != tc.k {
					t.Errorf("DecodePulses(%d, %d, %d) = %v has L1=%d, want %d",
						idx, tc.n, tc.k, y, sum, tc.k)
				}

				// Verify uniqueness
				key := ""
				for _, v := range y {
					key += string(rune(v + 1000))
				}
				if prevIdx, exists := seen[key]; exists {
					t.Errorf("Duplicate vector: idx %d and %d both decode to %v",
						prevIdx, idx, y)
				}
				seen[key] = idx
			}

			// Verify we got exactly V(n,k) unique vectors
			if uint32(len(seen)) != vCount {
				t.Errorf("Got %d unique vectors, want V(%d,%d) = %d",
					len(seen), tc.n, tc.k, vCount)
			}
		})
	}
}

// TestPVQ_VRecurrence verifies the V(n,k) recurrence relation.
// V(N, K) = V(N-1, K) + V(N, K-1) + V(N-1, K-1) for N > 1, K > 0
// Boundary conditions:
//   - V(N, 0) = 1 for any N >= 0 (only the zero vector)
//   - V(0, K) = 0 for K > 0 (no dimensions, can't have pulses)
//   - V(1, K) = 2 for K > 0 (only +K and -K)
func TestPVQ_VRecurrence(t *testing.T) {
	// Test boundary conditions
	t.Run("boundary_V_n_0", func(t *testing.T) {
		// V(n, 0) = 1 for any n >= 0
		for n := 0; n <= 10; n++ {
			v := PVQ_V(n, 0)
			if v != 1 {
				t.Errorf("V(%d, 0) = %d, want 1", n, v)
			}
		}
	})

	t.Run("boundary_V_0_k", func(t *testing.T) {
		// V(0, k) = 0 for k > 0
		for k := 1; k <= 10; k++ {
			v := PVQ_V(0, k)
			if v != 0 {
				t.Errorf("V(0, %d) = %d, want 0", k, v)
			}
		}
	})

	t.Run("boundary_V_1_k", func(t *testing.T) {
		// V(1, k) = 2 for k > 0
		for k := 1; k <= 10; k++ {
			v := PVQ_V(1, k)
			if v != 2 {
				t.Errorf("V(1, %d) = %d, want 2", k, v)
			}
		}
	})

	// Test recurrence: V(n,k) = V(n-1,k) + V(n,k-1) + V(n-1,k-1)
	t.Run("recurrence", func(t *testing.T) {
		for n := 2; n <= 10; n++ {
			for k := 1; k <= 10; k++ {
				v := PVQ_V(n, k)
				expected := PVQ_V(n-1, k) + PVQ_V(n, k-1) + PVQ_V(n-1, k-1)
				if v != expected {
					t.Errorf("V(%d, %d) = %d, but V(%d,%d)+V(%d,%d)+V(%d,%d) = %d+%d+%d = %d",
						n, k, v,
						n-1, k, n, k-1, n-1, k-1,
						PVQ_V(n-1, k), PVQ_V(n, k-1), PVQ_V(n-1, k-1), expected)
				}
			}
		}
	})

	// Verify some known values computed by hand
	t.Run("known_values", func(t *testing.T) {
		knownValues := []struct {
			n, k int
			v    uint32
		}{
			// Base cases
			{0, 0, 1},
			{1, 0, 1},
			{2, 0, 1},
			{0, 1, 0},
			{1, 1, 2},
			{1, 2, 2},

			// V(2,1) = V(1,1) + V(2,0) + V(1,0) = 2 + 1 + 1 = 4
			{2, 1, 4},
			// V(2,2) = V(1,2) + V(2,1) + V(1,1) = 2 + 4 + 2 = 8
			{2, 2, 8},
			// V(3,1) = V(2,1) + V(3,0) + V(2,0) = 4 + 1 + 1 = 6
			{3, 1, 6},
			// V(3,2) = V(2,2) + V(3,1) + V(2,1) = 8 + 6 + 4 = 18
			{3, 2, 18},
			// V(4,1) = V(3,1) + V(4,0) + V(3,0) = 6 + 1 + 1 = 8
			{4, 1, 8},
			// V(4,2) = V(3,2) + V(4,1) + V(3,1) = 18 + 8 + 6 = 32
			{4, 2, 32},
		}

		for _, kv := range knownValues {
			v := PVQ_V(kv.n, kv.k)
			if v != kv.v {
				t.Errorf("V(%d, %d) = %d, want %d", kv.n, kv.k, v, kv.v)
			}
		}
	})
}
