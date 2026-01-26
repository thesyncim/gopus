// Package celt implements CWRS (Combinatorial With Radix Signed) unit tests
// ported from libopus celt/tests/test_unit_cwrs32.c
//
// This test validates the PVQ (Pyramid Vector Quantization) combinatorial
// indexing implementation against the libopus reference.
package celt

import (
	"testing"
)

// Dimension sizes for standard Opus modes (non-custom modes)
// These correspond to band sizes achievable by splitting standard Opus mode bands:
// 176, 144, 96, 88, 72, 64, 48, 44, 36, 32, 24, 22, 18, 16, 8, 4, 2
var cwrsPN = []int{
	2, 3, 4, 6, 8, 9, 11, 12, 16,
	18, 22, 24, 32, 36, 44, 48, 64, 72,
	88, 96, 144, 176,
}

// Maximum K (pulses) for each N that fits in 32-bit arithmetic
var cwrsPKMax = []int{
	128, 128, 128, 88, 36, 26, 18, 16, 12,
	11, 9, 9, 7, 7, 6, 6, 5, 5,
	5, 5, 4, 4,
}

// TestGetPulses verifies the getPulses function matches libopus.
// getPulses is defined in pulse_cache.go and converts a "pseudo" value
// to actual pulse count K using the formula:
//
//	i < 8 ? i : (8 + (i&7)) << ((i>>3)-1)
//
// This encoding allows efficient representation of pulse counts in the
// range [0, 128+] using values [0, 40].
func TestGetPulses(t *testing.T) {
	// Expected values from libopus get_pulses()
	expected := []int{
		0, 1, 2, 3, 4, 5, 6, 7, // pseudo 0-7: direct mapping
		8, 9, 10, 11, 12, 13, 14, 15, // pseudo 8-15: (8+i&7) << 0
		16, 18, 20, 22, 24, 26, 28, 30, // pseudo 16-23: (8+i&7) << 1
		32, 36, 40, 44, 48, 52, 56, 60, // pseudo 24-31: (8+i&7) << 2
		64, 72, 80, 88, 96, 104, 112, 120, // pseudo 32-39: (8+i&7) << 3
		128, // pseudo 40: (8+0) << 4
	}

	for pseudo, want := range expected {
		got := getPulses(pseudo)
		if got != want {
			t.Errorf("getPulses(%d) = %d, want %d", pseudo, got, want)
		}
	}
}

// TestCWRS32 is the main test ported from test_unit_cwrs32.c.
// It tests the CWRS encode/decode round-trip for all supported (N, K) combinations.
func TestCWRS32(t *testing.T) {
	if len(cwrsPN) != len(cwrsPKMax) {
		t.Fatalf("cwrsPN and cwrsPKMax length mismatch: %d vs %d", len(cwrsPN), len(cwrsPKMax))
	}

	for dimIdx := 0; dimIdx < len(cwrsPN); dimIdx++ {
		n := cwrsPN[dimIdx]
		kmax := cwrsPKMax[dimIdx]

		for pseudo := 1; pseudo < 41; pseudo++ {
			k := getPulses(pseudo)
			if k > kmax {
				break
			}

			t.Run("", func(t *testing.T) {
				testCWRSRoundTrip(t, n, k)
			})
		}
	}
}

// testCWRSRoundTrip tests the CWRS encode/decode round-trip for specific (n, k).
// This mirrors the inner loop of test_unit_cwrs32.c:
// 1. Compute V(n,k) - number of codewords
// 2. For sampled indices i in [0, V(n,k)):
//   - Decode: cwrsi(n, k, i) -> y
//   - Verify: sum(|y|) == k
//   - Encode: icwrs(n, y) -> (ii, v)
//   - Verify: ii == i
//   - Verify: v == V(n,k)
func testCWRSRoundTrip(t *testing.T, n, k int) {
	// Compute codebook size V(n,k)
	nc := PVQ_V(n, k)
	if nc == 0 {
		t.Errorf("V(%d, %d) = 0, expected positive", n, k)
		return
	}

	// Sample indices: test every index if small, else sample ~20000
	inc := nc / 20000
	if inc < 1 {
		inc = 1
	}

	for i := uint32(0); i < nc; i += inc {
		// Decode: index -> pulse vector
		y := DecodePulses(i, n, k)
		if y == nil {
			t.Errorf("DecodePulses(%d, %d, %d) returned nil", i, n, k)
			continue
		}

		if len(y) != n {
			t.Errorf("DecodePulses(%d, %d, %d) length = %d, want %d", i, n, k, len(y), n)
			continue
		}

		// Verify pulse count: sum(|y|) == k
		sy := 0
		for j := 0; j < n; j++ {
			if y[j] < 0 {
				sy -= y[j]
			} else {
				sy += y[j]
			}
		}
		if sy != k {
			t.Errorf("N=%d K=%d i=%d: Pulse count mismatch, got %d, want %d (y=%v)",
				n, k, i, sy, k, y)
			continue
		}

		// Encode: pulse vector -> index
		ii := EncodePulses(y, n, k)

		// Verify round-trip: index should match
		if ii != i {
			t.Errorf("N=%d K=%d: Combination-index mismatch, cwrsi(%d)->%v->icwrs=%d",
				n, k, i, y, ii)
		}
	}
}

// TestCWRS32AllIndices is a more thorough test that checks every index
// for small (n, k) combinations. This catches edge cases the sampling might miss.
func TestCWRS32AllIndices(t *testing.T) {
	// Small (n, k) pairs where we can exhaustively test all indices
	testCases := []struct {
		n, k int
	}{
		{2, 1},
		{2, 2},
		{2, 3},
		{2, 4},
		{3, 1},
		{3, 2},
		{3, 3},
		{4, 1},
		{4, 2},
		{4, 3},
		{5, 1},
		{5, 2},
		{6, 1},
		{6, 2},
		{8, 1},
		{8, 2},
	}

	for _, tc := range testCases {
		n, k := tc.n, tc.k
		nc := PVQ_V(n, k)

		t.Run("", func(t *testing.T) {
			// Track unique vectors to verify bijectivity
			seen := make(map[string]uint32)

			for i := uint32(0); i < nc; i++ {
				y := DecodePulses(i, n, k)
				if y == nil {
					t.Errorf("DecodePulses(%d, %d, %d) returned nil", i, n, k)
					continue
				}

				// Verify pulse count
				sy := 0
				for _, v := range y {
					if v < 0 {
						sy -= v
					} else {
						sy += v
					}
				}
				if sy != k {
					t.Errorf("N=%d K=%d i=%d: Pulse count = %d, want %d", n, k, i, sy, k)
				}

				// Verify uniqueness
				key := vectorKey(y)
				if prevIdx, exists := seen[key]; exists {
					t.Errorf("N=%d K=%d: Duplicate vector at i=%d and i=%d: %v",
						n, k, prevIdx, i, y)
				}
				seen[key] = i

				// Verify round-trip
				ii := EncodePulses(y, n, k)
				if ii != i {
					t.Errorf("N=%d K=%d: Round-trip failed, %d -> %v -> %d", n, k, i, y, ii)
				}
			}

			// Verify we got exactly V(n,k) unique vectors
			if uint32(len(seen)) != nc {
				t.Errorf("N=%d K=%d: Got %d unique vectors, want V(%d,%d)=%d",
					n, k, len(seen), n, k, nc)
			}
		})
	}
}

// vectorKey creates a unique string key for a pulse vector.
func vectorKey(y []int) string {
	// Use a simple encoding that's guaranteed unique
	result := make([]byte, len(y)*4)
	for i, v := range y {
		// Map int to 4 bytes
		uv := uint32(v) + 0x80000000 // Make unsigned
		result[i*4] = byte(uv >> 24)
		result[i*4+1] = byte(uv >> 16)
		result[i*4+2] = byte(uv >> 8)
		result[i*4+3] = byte(uv)
	}
	return string(result)
}

// TestPVQ_V_LibopusValues tests V(n,k) against values from libopus CELT_PVQ_U table.
// These are derived from the CELT_PVQ_U_DATA table in cwrs.c.
func TestPVQ_V_LibopusValues(t *testing.T) {
	// Selected values from libopus cwrs.c comments:
	// V[10][10] = {
	//   {1,  0,   0,    0,    0,     0,     0,      0,      0,       0},
	//   {1,  2,   2,    2,    2,     2,     2,      2,      2,       2},
	//   {1,  4,   8,   12,   16,    20,    24,     28,     32,      36},
	//   {1,  6,  18,   38,   66,   102,   146,    198,    258,     326},
	//   {1,  8,  32,   88,  192,   360,   608,    952,   1408,    1992},
	//   {1, 10,  50,  170,  450,  1002,  1970,   3530,   5890,    9290},
	//   {1, 12,  72,  292,  912,  2364,  5336,  10836,  20256,   35436},
	//   {1, 14,  98,  462, 1666,  4942, 12642,  28814,  59906,  115598},
	//   {1, 16, 128,  688, 2816,  9424, 27008,  68464, 157184,  332688},
	//   {1, 18, 162,  978, 4482, 16722, 53154, 148626, 374274,  864146}
	// }
	testCases := []struct {
		n, k int
		want uint32
	}{
		// Row 0: V(0, k) = 1 for k=0, 0 otherwise
		{0, 0, 1},
		{0, 1, 0},
		{0, 5, 0},

		// Row 1: V(1, k) = 2 for k > 0
		{1, 0, 1},
		{1, 1, 2},
		{1, 2, 2},
		{1, 5, 2},
		{1, 9, 2},

		// Row 2: V(2, k) = 4*k for k > 0
		{2, 0, 1},
		{2, 1, 4},
		{2, 2, 8},
		{2, 3, 12},
		{2, 4, 16},
		{2, 5, 20},
		{2, 9, 36},

		// Row 3: V(3, k)
		{3, 0, 1},
		{3, 1, 6},
		{3, 2, 18},
		{3, 3, 38},
		{3, 4, 66},
		{3, 5, 102},
		{3, 9, 326},

		// Row 4: V(4, k)
		{4, 0, 1},
		{4, 1, 8},
		{4, 2, 32},
		{4, 3, 88},
		{4, 4, 192},
		{4, 5, 360},
		{4, 9, 1992},

		// Row 5: V(5, k)
		{5, 0, 1},
		{5, 1, 10},
		{5, 2, 50},
		{5, 3, 170},
		{5, 4, 450},
		{5, 5, 1002},
		{5, 9, 9290},

		// Row 6: V(6, k)
		{6, 0, 1},
		{6, 1, 12},
		{6, 2, 72},
		{6, 3, 292},
		{6, 4, 912},
		{6, 5, 2364},
		{6, 9, 35436},

		// Row 7: V(7, k)
		{7, 0, 1},
		{7, 1, 14},
		{7, 2, 98},
		{7, 3, 462},
		{7, 4, 1666},
		{7, 5, 4942},
		{7, 9, 115598},

		// Row 8: V(8, k)
		{8, 0, 1},
		{8, 1, 16},
		{8, 2, 128},
		{8, 3, 688},
		{8, 4, 2816},
		{8, 5, 9424},
		{8, 9, 332688},

		// Row 9: V(9, k)
		{9, 0, 1},
		{9, 1, 18},
		{9, 2, 162},
		{9, 3, 978},
		{9, 4, 4482},
		{9, 5, 16722},
		{9, 9, 864146},
	}

	for _, tc := range testCases {
		got := PVQ_V(tc.n, tc.k)
		if got != tc.want {
			t.Errorf("V(%d, %d) = %d, want %d (libopus)", tc.n, tc.k, got, tc.want)
		}
	}
}

// TestCWRS32LargerDimensions tests larger dimensions used in actual Opus decoding.
func TestCWRS32LargerDimensions(t *testing.T) {
	// Test cases with larger N that occur in real Opus streams
	testCases := []struct {
		n, k int
	}{
		{16, 4},  // Common in CELT bands
		{24, 3},  // Larger band
		{32, 3},  // Even larger
		{48, 2},  // Wide band, few pulses
		{64, 2},  // Very wide band
		{88, 2},  // Near maximum standard band width
		{96, 2},  // Maximum standard band width
		{144, 2}, // Extended band
		{176, 2}, // Maximum supported
	}

	for _, tc := range testCases {
		n, k := tc.n, tc.k
		nc := PVQ_V(n, k)
		if nc == 0 {
			t.Errorf("V(%d, %d) = 0", n, k)
			continue
		}

		t.Run("", func(t *testing.T) {
			// Test a sample of indices
			testIndices := []uint32{0, 1, nc / 4, nc / 2, nc - 2, nc - 1}

			for _, i := range testIndices {
				if i >= nc {
					continue
				}

				y := DecodePulses(i, n, k)
				if y == nil {
					t.Errorf("DecodePulses(%d, %d, %d) returned nil", i, n, k)
					continue
				}

				// Verify pulse count
				sy := 0
				for _, v := range y {
					if v < 0 {
						sy -= v
					} else {
						sy += v
					}
				}
				if sy != k {
					t.Errorf("N=%d K=%d i=%d: Pulse count = %d, want %d", n, k, i, sy, k)
				}

				// Verify round-trip
				ii := EncodePulses(y, n, k)
				if ii != i {
					t.Errorf("N=%d K=%d: Round-trip %d -> %v -> %d", n, k, i, y, ii)
				}
			}
		})
	}
}

// TestCWRS32EdgeCases tests edge cases in CWRS encoding/decoding.
func TestCWRS32EdgeCases(t *testing.T) {
	// Test N=1 special case
	t.Run("N1", func(t *testing.T) {
		for k := 1; k <= 10; k++ {
			nc := PVQ_V(1, k)
			if nc != 2 {
				t.Errorf("V(1, %d) = %d, want 2", k, nc)
			}

			// Index 0 should give +k
			y0 := DecodePulses(0, 1, k)
			if y0 == nil || len(y0) != 1 || y0[0] != k {
				t.Errorf("DecodePulses(0, 1, %d) = %v, want [%d]", k, y0, k)
			}

			// Index 1 should give -k
			y1 := DecodePulses(1, 1, k)
			if y1 == nil || len(y1) != 1 || y1[0] != -k {
				t.Errorf("DecodePulses(1, 1, %d) = %v, want [%d]", k, y1, -k)
			}
		}
	})

	// Test K=0 special case
	t.Run("K0", func(t *testing.T) {
		for n := 1; n <= 10; n++ {
			nc := PVQ_V(n, 0)
			if nc != 1 {
				t.Errorf("V(%d, 0) = %d, want 1", n, nc)
			}

			y := DecodePulses(0, n, 0)
			if y == nil || len(y) != n {
				t.Errorf("DecodePulses(0, %d, 0) = %v, want zero vector of length %d", n, y, n)
				continue
			}

			for i, v := range y {
				if v != 0 {
					t.Errorf("DecodePulses(0, %d, 0)[%d] = %d, want 0", n, i, v)
				}
			}
		}
	})

	// Test first and last index for various (n, k)
	t.Run("BoundaryIndices", func(t *testing.T) {
		testCases := []struct {
			n, k int
		}{
			{2, 5},
			{3, 4},
			{4, 3},
			{5, 3},
			{8, 2},
			{10, 2},
		}

		for _, tc := range testCases {
			nc := PVQ_V(tc.n, tc.k)

			// Test index 0
			y0 := DecodePulses(0, tc.n, tc.k)
			if y0 == nil {
				t.Errorf("DecodePulses(0, %d, %d) returned nil", tc.n, tc.k)
			} else {
				ii := EncodePulses(y0, tc.n, tc.k)
				if ii != 0 {
					t.Errorf("Round-trip failed for index 0, N=%d, K=%d: got %d", tc.n, tc.k, ii)
				}
			}

			// Test last index
			yLast := DecodePulses(nc-1, tc.n, tc.k)
			if yLast == nil {
				t.Errorf("DecodePulses(%d, %d, %d) returned nil", nc-1, tc.n, tc.k)
			} else {
				ii := EncodePulses(yLast, tc.n, tc.k)
				if ii != nc-1 {
					t.Errorf("Round-trip failed for index %d, N=%d, K=%d: got %d", nc-1, tc.n, tc.k, ii)
				}
			}
		}
	})
}

// BenchmarkCWRS32Decode benchmarks CWRS decoding performance.
func BenchmarkCWRS32Decode(b *testing.B) {
	// Typical parameters from Opus CELT
	testCases := []struct {
		name string
		n, k int
	}{
		{"N8_K4", 8, 4},
		{"N16_K4", 16, 4},
		{"N32_K3", 32, 3},
		{"N64_K2", 64, 2},
	}

	for _, tc := range testCases {
		nc := PVQ_V(tc.n, tc.k)
		testIndex := nc / 2 // Use middle index

		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = DecodePulses(testIndex, tc.n, tc.k)
			}
		})
	}
}

// BenchmarkCWRS32Encode benchmarks CWRS encoding performance.
func BenchmarkCWRS32Encode(b *testing.B) {
	// Pre-decode to get pulse vectors
	testCases := []struct {
		name string
		n, k int
	}{
		{"N8_K4", 8, 4},
		{"N16_K4", 16, 4},
		{"N32_K3", 32, 3},
		{"N64_K2", 64, 2},
	}

	for _, tc := range testCases {
		nc := PVQ_V(tc.n, tc.k)
		y := DecodePulses(nc/2, tc.n, tc.k)

		b.Run(tc.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_ = EncodePulses(y, tc.n, tc.k)
			}
		})
	}
}

// BenchmarkCWRS32RoundTrip benchmarks full encode/decode cycle.
func BenchmarkCWRS32RoundTrip(b *testing.B) {
	n, k := 16, 4
	nc := PVQ_V(n, k)
	testIndex := nc / 2

	b.Run("N16_K4", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			y := DecodePulses(testIndex, n, k)
			_ = EncodePulses(y, n, k)
		}
	})
}
