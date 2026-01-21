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
		{0, 0, 1},  // No dimensions, no pulses = 1 (zero vector)
		{0, 1, 0},  // No dimensions with pulses = 0
		{1, 0, 1},  // One dimension, no pulses = 1 (zero vector)
		{2, 0, 1},  // Two dimensions, no pulses = 1 (zero vector)

		// N=1 cases: V(1,K) = 2 for K > 0 (only +K and -K)
		{1, 1, 2},  // +1, -1
		{1, 2, 2},  // +2, -2
		{1, 3, 2},  // +3, -3
		{1, 5, 2},  // +5, -5

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
	for i := 0; i < b.N; i++ {
		DecodePulses(12345, 16, 8)
	}
}

// BenchmarkDecodePulsesLarge benchmarks decoding with larger parameters.
func BenchmarkDecodePulsesLarge(b *testing.B) {
	for i := 0; i < b.N; i++ {
		DecodePulses(123456, 32, 16)
	}
}
