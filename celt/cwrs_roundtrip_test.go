// Package celt contains a comprehensive roundtrip test for CWRS encoding.
// This test verifies that encode(pulses) -> decode == original pulses.
package celt

import (
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestCWRSRoundtripWithRangeCoding tests the full CWRS encode/decode path
// using actual range coding (as used in the encoder).
// This matches how algQuant and algUnquant work.
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
		inc := nc / 100
		if inc < 1 {
			inc = 1
		}

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
			_ = cwrsi(n, k, decodedIndex, decodedPulses, u2)

			// Verify pulse vectors match
			for i := 0; i < n; i++ {
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
		maxTest := nc
		if maxTest > 1000 {
			maxTest = 1000
		}
		inc := nc / maxTest
		if inc < 1 {
			inc = 1
		}

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

	for n := 0; n < 10; n++ {
		for k := 0; k < 10; k++ {
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

			for idx := uint32(0); idx < nc; idx++ {
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
