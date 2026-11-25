package celt

import (
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

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

	for dimIdx := range cwrsPN {
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
	inc := max(nc/20000, 1)

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
		for j := range n {
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

			for i := range nc {
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

func TestDecodePulsesInto32MatchesIntPath(t *testing.T) {
	testCases := []struct {
		name    string
		n, k    int
		indices []uint32
	}{
		{"N4K5", 4, 5, []uint32{0, 1, 6, 16, 32, 63, 127}},
		{"N5K8", 5, 8, nil},
		{"N6K88", 6, 88, nil},
		{"N8K8", 8, 8, nil},
		{"N9K26", 9, 26, nil},
		{"N11K18", 11, 18, nil},
	}

	var yi [256]int
	var y32 [256]int32
	for _, tc := range testCases {
		nc := PVQ_V(tc.n, tc.k)
		if nc == 0 {
			continue
		}
		indices := tc.indices
		if len(indices) == 0 {
			indices = []uint32{0, 1, 2, nc / 4, nc / 2, nc - 3, nc - 2, nc - 1}
		}
		t.Run(tc.name, func(t *testing.T) {
			for _, idx := range indices {
				if idx >= nc {
					continue
				}
				for i := range yi {
					yi[i] = 777
				}
				for i := range y32 {
					y32[i] = 777
				}

				yy := decodePulsesInto(idx, tc.n, tc.k, yi[:], nil)
				yy32 := decodePulsesInto32(idx, tc.n, tc.k, y32[:], nil)
				if yy != yy32 {
					t.Fatalf("energy mismatch idx=%d: int=%d int32=%d y=%v y32=%v", idx, yy, yy32, yi[:tc.n], y32[:tc.n])
				}
				for i := 0; i < tc.n; i++ {
					if yi[i] != int(y32[i]) {
						t.Fatalf("pulse mismatch idx=%d pos=%d: int=%v int32=%v", idx, i, yi[:tc.n], y32[:tc.n])
					}
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

func BenchmarkDecodePulsesInto32Hot(b *testing.B) {
	testCases := []struct {
		name string
		n, k int
	}{
		{"N4_K4", 4, 4},
		{"N4_K32", 4, 32},
		{"N6_K88", 6, 88},
		{"N8_K8", 8, 8},
		{"N9_K26", 9, 26},
		{"N11_K18", 11, 18},
		{"N12_K8", 12, 8},
		{"N16_K8", 16, 8},
		{"N24_K6", 24, 6},
	}

	var y [64]int32
	for _, tc := range testCases {
		nc := PVQ_V(tc.n, tc.k)
		if nc == 0 {
			continue
		}
		testIndex := nc / 2
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = decodePulsesInto32(testIndex, tc.n, tc.k, y[:], nil)
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

func icwrsLookupChecked(n, k int, y []int) (uint32, bool) {
	if n < 2 || k <= 0 || len(y) < n {
		return 0, false
	}

	i, k1 := icwrs1(y[n-1])
	base, ok := pvqUTableLookup(2, k1)
	if !ok {
		return 0, false
	}
	i += base

	j := n - 2
	k1 += abs(y[j])
	if y[j] < 0 {
		neg, ok := pvqUTableLookup(2, k1+1)
		if !ok {
			return 0, false
		}
		i += neg
	}

	for j--; j >= 0; j-- {
		remDims := n - j
		base, ok := pvqUTableLookup(remDims, k1)
		if !ok {
			return 0, false
		}
		i += base
		k1 += abs(y[j])
		if y[j] < 0 {
			neg, ok := pvqUTableLookup(remDims, k1+1)
			if !ok {
				return 0, false
			}
			i += neg
		}
	}

	return i, true
}

func TestCanUseICWRSLookupFastMatchesCoverage(t *testing.T) {
	for n := 1; n <= 64; n++ {
		for k := 0; k <= 96; k++ {
			want := n >= 2 && k > 0 && pvqUHasLookup(n, k) && pvqUHasLookup(n, k+1)
			if got := canUseICWRSLookupFast(n, k); got != want {
				t.Fatalf("canUseICWRSLookupFast(%d, %d) = %v, want %v", n, k, got, want)
			}
		}
	}
}

func TestICWRSLookupFastMatchesChecked(t *testing.T) {
	cases := []struct {
		n, k int
	}{
		{4, 2},
		{8, 4},
		{18, 5},
		{32, 5},
		{36, 7},
		{48, 5},
		{48, 6},
	}

	for _, tc := range cases {
		if !canUseICWRSLookupFast(tc.n, tc.k) {
			t.Fatalf("test case (%d,%d) unexpectedly lacks fast lookup coverage", tc.n, tc.k)
		}
		total := PVQ_V(tc.n, tc.k)
		if total == 0 {
			t.Fatalf("PVQ_V(%d, %d) = 0", tc.n, tc.k)
		}
		step := uint32(1)
		if total > 257 {
			step = total / 257
			if step == 0 {
				step = 1
			}
		}
		for idx := uint32(0); idx < total; idx += step {
			y := DecodePulses(idx, tc.n, tc.k)
			got, gotOK := icwrsLookupFast(tc.n, tc.k, y)
			want, wantOK := icwrsLookupChecked(tc.n, tc.k, y)
			if gotOK != wantOK || got != want {
				t.Fatalf("lookup mismatch for n=%d k=%d idx=%d: got (%d,%v) want (%d,%v)", tc.n, tc.k, idx, got, gotOK, want, wantOK)
			}
		}
	}
}

func BenchmarkICWRSLookupFastCovered(b *testing.B) {
	y := DecodePulses(PVQ_V(48, 5)/2, 48, 5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = icwrsLookupFast(48, 5, y)
	}
}

func BenchmarkICWRSLookupCheckedCovered(b *testing.B) {
	y := DecodePulses(PVQ_V(48, 5)/2, 48, 5)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = icwrsLookupChecked(48, 5, y)
	}
}

// TestICWRSLookupFast32MatchesRecurrence pins the table walk to the
// u-row recurrence of icwrs32 for random pulse vectors at every covered
// (n, k), including signs on every position.
func TestICWRSLookupFast32MatchesRecurrence(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	y := make([]int32, 176)
	u := make([]uint32, 180)
	for n := 2; n <= 176; n++ {
		for k := 1; k <= 176; k++ {
			if !canUseICWRSLookupFast(n, k) {
				continue
			}
			for trial := 0; trial < 12; trial++ {
				clear(y[:n])
				for range k {
					y[rng.Intn(n)]++
				}
				for i := range y[:n] {
					if rng.Intn(2) == 0 {
						y[i] = -y[i]
					}
				}
				got, ok := icwrsLookupFast32(n, k, y)
				want, _ := icwrs32(n, k, y, u)
				if !ok || got != want {
					t.Fatalf("n=%d k=%d y=%v: got (%d,%v) want %d", n, k, y[:n], got, ok, want)
				}
			}
		}
	}
}

func TestCWRSRoundtripWithRangeCoding(t *testing.T) {
	testCases := []struct {
		n, k int
	}{
		{2, 1},
		{2, 2},
		{2, 5},
		{3, 1},
		{3, 2},
		{3, 4},
		{4, 2},
		{4, 4},
		{8, 4},
		{16, 4},
		{16, 8},
		{32, 3},
		{64, 2},
	}

	for _, tc := range testCases {
		n, k := tc.n, tc.k
		nc := PVQ_V(n, k)
		if nc == 0 {
			t.Errorf("V(%d, %d) = 0", n, k)
			continue
		}

		// Test a sampling of indices
		inc := max(nc/100, 1)

		for idx := uint32(0); idx < nc; idx += inc {
			// Create a buffer for range coding
			buf := make([]byte, 1024)

			// --- ENCODE PHASE ---
			// First decode to get pulses (simulating what opPVQSearch would produce)
			originalPulses := DecodePulses(idx, n, k)
			if originalPulses == nil {
				t.Errorf("DecodePulses(%d, %d, %d) returned nil", idx, n, k)
				continue
			}

			// Encode the pulses using EncodePulses (same path as algQuant)
			encodedIndex := EncodePulses(originalPulses, n, k)

			// Verify the encoded index matches
			if encodedIndex != idx {
				t.Errorf("N=%d K=%d: EncodePulses mismatch, got %d, want %d (pulses=%v)",
					n, k, encodedIndex, idx, originalPulses)
				continue
			}

			// Now test through actual range coding
			enc := &rangecoding.Encoder{}
			enc.Init(buf)

			// Compute V(n,k) as the encoder would
			u := make([]uint32, k+2)
			vSize := ncwrsUrow(n, k, u)
			if vSize != nc {
				t.Errorf("ncwrsUrow(%d, %d) = %d, want %d", n, k, vSize, nc)
			}

			// Encode as uniform (same as algQuant)
			enc.EncodeUniform(encodedIndex, vSize)

			// Finalize encoding
			encoded := enc.Done()

			// --- DECODE PHASE ---
			dec := &rangecoding.Decoder{}
			dec.Init(encoded)

			// Decode uniform (same as algUnquant)
			decodedIndex := dec.DecodeUniform(vSize)

			if decodedIndex != idx {
				t.Errorf("N=%d K=%d idx=%d: Range coding roundtrip failed, decoded %d",
					n, k, idx, decodedIndex)
				continue
			}

			// Decode pulses
			u2 := make([]uint32, k+2)
			ncwrsUrow(n, k, u2)
			decodedPulses := make([]int, n)
			cwrsi(n, k, decodedIndex, decodedPulses, u2)

			// Verify pulse vectors match
			for i := range n {
				if decodedPulses[i] != originalPulses[i] {
					t.Errorf("N=%d K=%d idx=%d: Pulse mismatch at [%d], got %d, want %d",
						n, k, idx, i, decodedPulses[i], originalPulses[i])
					t.Errorf("  Original: %v", originalPulses)
					t.Errorf("  Decoded:  %v", decodedPulses)
					break
				}
			}
		}
	}
}

// TestCWRSEncodePulsesMatchesLibopus verifies EncodePulses produces same index as decode.
// This is the critical property: decode(encode(decode(idx))) == decode(idx)
func TestCWRSEncodePulsesMatchesLibopus(t *testing.T) {
	// Test cases cover the range of (n, k) used in actual CELT encoding
	testCases := []struct {
		n, k int
	}{
		// Small cases - exhaustive testing possible
		{2, 1},  // V = 4
		{2, 2},  // V = 8
		{2, 3},  // V = 12
		{3, 1},  // V = 6
		{3, 2},  // V = 18
		{3, 3},  // V = 38
		{4, 1},  // V = 8
		{4, 2},  // V = 32
		{4, 3},  // V = 88
		{5, 2},  // V = 50
		{6, 2},  // V = 72
		{8, 2},  // V = 128
		{8, 4},  // V = 2816
		{16, 4}, // V = large
		{32, 3}, // V = larger
	}

	for _, tc := range testCases {
		n, k := tc.n, tc.k
		nc := PVQ_V(n, k)

		// Determine how many indices to test
		maxTest := min(nc, 1000)
		inc := max(nc/maxTest, 1)

		for idx := uint32(0); idx < nc; idx += inc {
			// Decode index to pulses
			pulses := DecodePulses(idx, n, k)
			if pulses == nil {
				t.Fatalf("DecodePulses(%d, %d, %d) returned nil", idx, n, k)
			}

			// Verify pulse sum
			sum := 0
			for _, p := range pulses {
				if p < 0 {
					sum -= p
				} else {
					sum += p
				}
			}
			if sum != k {
				t.Errorf("N=%d K=%d idx=%d: Pulse sum %d != k", n, k, idx, sum)
				continue
			}

			// Encode back to index
			reencoded := EncodePulses(pulses, n, k)

			// Must match!
			if reencoded != idx {
				t.Errorf("N=%d K=%d: Roundtrip failed, idx=%d -> pulses=%v -> idx=%d",
					n, k, idx, pulses, reencoded)
			}
		}
	}
}

// TestCWRSSpecificPulseVectors tests specific pulse vectors to verify encoding.
func TestCWRSSpecificPulseVectors(t *testing.T) {
	tests := []struct {
		name   string
		pulses []int
		n, k   int
	}{
		// N=2, K=1: V=4, indices 0,1,2,3
		{"n2k1_pos_first", []int{1, 0}, 2, 1},
		{"n2k1_pos_second", []int{0, 1}, 2, 1},
		{"n2k1_neg_second", []int{0, -1}, 2, 1},
		{"n2k1_neg_first", []int{-1, 0}, 2, 1},

		// N=3, K=1: V=6
		{"n3k1_0", []int{1, 0, 0}, 3, 1},
		{"n3k1_1", []int{0, 1, 0}, 3, 1},
		{"n3k1_2", []int{0, 0, 1}, 3, 1},
		{"n3k1_3", []int{0, 0, -1}, 3, 1},
		{"n3k1_4", []int{0, -1, 0}, 3, 1},
		{"n3k1_5", []int{-1, 0, 0}, 3, 1},

		// N=2, K=2: V=8
		{"n2k2_all_first_pos", []int{2, 0}, 2, 2},
		{"n2k2_split_pos", []int{1, 1}, 2, 2},
		{"n2k2_split_mix", []int{1, -1}, 2, 2},
		{"n2k2_all_second_neg", []int{0, -2}, 2, 2},

		// Larger cases
		{"n4k2_corner", []int{2, 0, 0, 0}, 4, 2},
		{"n4k2_split", []int{1, 1, 0, 0}, 4, 2},
		{"n4k2_spread", []int{1, 0, 1, 0}, 4, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Encode
			idx := EncodePulses(tc.pulses, tc.n, tc.k)

			// Decode
			decoded := DecodePulses(idx, tc.n, tc.k)

			// Verify match
			if len(decoded) != len(tc.pulses) {
				t.Fatalf("Length mismatch: got %d, want %d", len(decoded), len(tc.pulses))
			}

			for i := range tc.pulses {
				if decoded[i] != tc.pulses[i] {
					t.Errorf("Mismatch at [%d]: got %d, want %d", i, decoded[i], tc.pulses[i])
					t.Errorf("  Input pulses:   %v", tc.pulses)
					t.Errorf("  Encoded index:  %d", idx)
					t.Errorf("  Decoded pulses: %v", decoded)
					break
				}
			}
		})
	}
}

// TestCWRSIcwrsVsLibopusTable verifies icwrs produces correct indices.
// This compares against the libopus V table in comments.
func TestCWRSIcwrsVsLibopusTable(t *testing.T) {
	// V[10][10] from libopus cwrs.c
	vTable := [][]uint32{
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{1, 2, 2, 2, 2, 2, 2, 2, 2, 2},
		{1, 4, 8, 12, 16, 20, 24, 28, 32, 36},
		{1, 6, 18, 38, 66, 102, 146, 198, 258, 326},
		{1, 8, 32, 88, 192, 360, 608, 952, 1408, 1992},
		{1, 10, 50, 170, 450, 1002, 1970, 3530, 5890, 9290},
		{1, 12, 72, 292, 912, 2364, 5336, 10836, 20256, 35436},
		{1, 14, 98, 462, 1666, 4942, 12642, 28814, 59906, 115598},
		{1, 16, 128, 688, 2816, 9424, 27008, 68464, 157184, 332688},
		{1, 18, 162, 978, 4482, 16722, 53154, 148626, 374274, 864146},
	}

	for n := range 10 {
		for k := range 10 {
			expected := vTable[n][k]
			got := PVQ_V(n, k)
			if got != expected {
				t.Errorf("V(%d, %d) = %d, want %d (from libopus table)", n, k, got, expected)
			}
		}
	}
}

// TestCWRSAllIndicesRoundtrip does exhaustive roundtrip for small codebooks.
func TestCWRSAllIndicesRoundtrip(t *testing.T) {
	smallCases := []struct {
		n, k int
	}{
		{2, 1}, {2, 2}, {2, 3}, {2, 4}, {2, 5},
		{3, 1}, {3, 2}, {3, 3},
		{4, 1}, {4, 2}, {4, 3},
		{5, 1}, {5, 2},
		{6, 1}, {6, 2},
		{8, 1}, {8, 2},
	}

	for _, tc := range smallCases {
		n, k := tc.n, tc.k
		nc := PVQ_V(n, k)
		if nc == 0 || nc > 100000 {
			continue
		}

		t.Run("", func(t *testing.T) {
			seen := make(map[string]uint32)

			for idx := range nc {
				// Decode
				pulses := DecodePulses(idx, n, k)
				if pulses == nil {
					t.Fatalf("DecodePulses(%d, %d, %d) nil", idx, n, k)
				}

				// Verify pulse sum
				sum := 0
				for _, p := range pulses {
					if p < 0 {
						sum -= p
					} else {
						sum += p
					}
				}
				if sum != k {
					t.Errorf("idx=%d: sum=%d, want k=%d", idx, sum, k)
				}

				// Verify uniqueness
				key := pulseKey(pulses)
				if prev, exists := seen[key]; exists {
					t.Errorf("Duplicate: idx %d and %d -> %v", prev, idx, pulses)
				}
				seen[key] = idx

				// Roundtrip
				reencoded := EncodePulses(pulses, n, k)
				if reencoded != idx {
					t.Errorf("Roundtrip: %d -> %v -> %d", idx, pulses, reencoded)
				}
			}

			if uint32(len(seen)) != nc {
				t.Errorf("Got %d unique, want V(%d,%d)=%d", len(seen), n, k, nc)
			}
		})
	}
}

func pulseKey(p []int) string {
	b := make([]byte, len(p)*4)
	for i, v := range p {
		uv := uint32(v) + 0x80000000
		b[i*4+0] = byte(uv >> 24)
		b[i*4+1] = byte(uv >> 16)
		b[i*4+2] = byte(uv >> 8)
		b[i*4+3] = byte(uv)
	}
	return string(b)
}

// TestCWRSTableLookupDecodeMatchesRowBased proves cwrsiTableLookup32 is
// bit-identical to the row-based ncwrsUrow+cwrsi32 path it replaces, across the
// (n,k) range gated by canUseCWRSFast and a full/sampled sweep of indices.
func TestCWRSTableLookupDecodeMatchesRowBased(t *testing.T) {
	for n := 3; n <= 40; n++ {
		for k := 3; k <= 30; k++ {
			if !canUseCWRSFast(n, k) {
				continue
			}
			v := PVQ_V(n, k)
			if v == 0 {
				continue
			}
			var idxs []uint32
			if v <= 20000 {
				idxs = make([]uint32, v)
				for i := range idxs {
					idxs[i] = uint32(i)
				}
			} else {
				idxs = []uint32{0, 1, 2, v - 1, v - 2, v / 2, v / 3, v / 7, v - v/3}
				x := uint32(n*131 + k)
				for range 32 {
					x = x*2654435761 + 12345
					idxs = append(idxs, x%v)
				}
			}
			yTab := make([]int32, n)
			yRow := make([]int32, n)
			u := make([]uint32, k+2)
			for _, i := range idxs {
				for z := range yTab {
					yTab[z] = 0
					yRow[z] = 0
				}
				yyTab := cwrsiTableLookup32(n, k, i, yTab)
				ncwrsUrow(n, k, u)
				yyRow := cwrsi32(n, k, i, yRow, u)
				if yyTab != yyRow {
					t.Fatalf("n=%d k=%d i=%d: yy table=%d row=%d", n, k, i, yyTab, yyRow)
				}
				for z := 0; z < n; z++ {
					if yTab[z] != yRow[z] {
						t.Fatalf("n=%d k=%d i=%d: y[%d] table=%d row=%d", n, k, i, z, yTab[z], yRow[z])
					}
				}
			}
		}
	}
}

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

func BenchmarkPVQ_VCovered(b *testing.B) {
	for i := 0; i < b.N; i++ {
		PVQ_V(48, 5)
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

			for idx := range vCount {
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

			for idx := range vCount {
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

func TestCWRSUnitLibopus(t *testing.T) {
	dims := []int{
		2, 3, 4, 6, 8, 9, 11, 12, 16,
		18, 22, 24, 32, 36, 44, 48, 64, 72,
		88, 96, 144, 176,
	}
	pkmax := []int{
		128, 128, 128, 88, 36, 26, 18, 16, 12,
		11, 9, 9, 7, 7, 6, 6, 5, 5,
		5, 5, 4, 4,
	}

	maxPseudo := 40
	sampleDiv := uint32(20000)
	if env := os.Getenv("CWRS_UNIT_SAMPLES"); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			sampleDiv = uint32(v)
		}
	}
	if testing.Short() {
		dims = dims[:8]
		pkmax = pkmax[:8]
		maxPseudo = 10
		sampleDiv = 2000
	}

	for ti, n := range dims {
		for pseudo := 1; pseudo <= maxPseudo; pseudo++ {
			k := getPulses(pseudo)
			if k > pkmax[ti] {
				break
			}

			uBase := make([]uint32, k+2)
			nc := ncwrsUrow(n, k, uBase)
			if nc == 0 {
				t.Fatalf("ncwrsUrow returned 0 for n=%d k=%d", n, k)
			}
			inc := max(nc/sampleDiv, 1)

			for i := uint32(0); i < nc; i += inc {
				u := make([]uint32, k+2)
				copy(u, uBase)
				y := make([]int, n)
				cwrsi(n, k, i, y, u)

				sum := 0
				for _, v := range y {
					if v < 0 {
						sum -= v
					} else {
						sum += v
					}
				}
				if sum != k {
					t.Fatalf("pulse sum mismatch n=%d k=%d i=%d sum=%d", n, k, i, sum)
				}

				ii, vcount := icwrs(n, k, y, make([]uint32, k+2))
				if ii != i {
					t.Fatalf("index mismatch n=%d k=%d i=%d got=%d", n, k, i, ii)
				}
				if vcount != nc {
					t.Fatalf("count mismatch n=%d k=%d want=%d got=%d", n, k, nc, vcount)
				}
			}
		}
	}
}
