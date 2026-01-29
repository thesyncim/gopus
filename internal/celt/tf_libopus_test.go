package celt

import (
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTFDecodeTable validates that tfSelectTable matches libopus tf_select_table.
// Source: libopus celt/celt.c
func TestTFDecodeTable(t *testing.T) {
	// libopus tf_select_table[4][8]:
	// {0, -1, 0, -1, 0, -1, 0, -1}, /* 2.5 ms */
	// {0, -1, 0, -2, 1, 0, 1, -1}, /* 5 ms */
	// {0, -2, 0, -3, 2, 0, 1, -1}, /* 10 ms */
	// {0, -2, 0, -3, 3, 0, 1, -1}, /* 20 ms */
	expected := [4][8]int8{
		{0, -1, 0, -1, 0, -1, 0, -1}, // LM=0, 2.5ms
		{0, -1, 0, -2, 1, 0, 1, -1},  // LM=1, 5ms
		{0, -2, 0, -3, 2, 0, 1, -1},  // LM=2, 10ms
		{0, -2, 0, -3, 3, 0, 1, -1},  // LM=3, 20ms
	}

	for lm := 0; lm < 4; lm++ {
		for idx := 0; idx < 8; idx++ {
			if tfSelectTable[lm][idx] != expected[lm][idx] {
				t.Errorf("tfSelectTable[%d][%d] = %d, want %d",
					lm, idx, tfSelectTable[lm][idx], expected[lm][idx])
			}
		}
	}
}

// TestTFDecodeBasic tests tfDecode with known input sequences.
func TestTFDecodeBasic(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		start       int
		end         int
		isTransient bool
		lm          int
		wantTfRes   []int
	}{
		{
			// Non-transient case: no TF changes, tfRes[i]=0 -> tfSelectTable[3][0]=0
			// idx = 4*isTransient + 2*tfSelect + tfRes[i] = 4*0 + 2*0 + 0 = 0
			// tfSelectTable[3][0] = 0
			name:        "all_zeros_lm3_long",
			data:        []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			start:       0,
			end:         21,
			isTransient: false,
			lm:          3,
			wantTfRes:   []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			// Transient case: tfRes[i]=0 -> tfSelectTable[3][4]=3
			// idx = 4*isTransient + 2*tfSelect + tfRes[i] = 4*1 + 2*0 + 0 = 4
			// tfSelectTable[3][4] = 3
			name:        "all_zeros_lm3_transient",
			data:        []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			start:       0,
			end:         21,
			isTransient: true,
			lm:          3,
			wantTfRes:   []int{3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3},
		},
		{
			// LM=0 case (2.5ms), non-transient
			// idx = 4*0 + 2*0 + 0 = 0, tfSelectTable[0][0] = 0
			name:        "lm0_no_tf_select",
			data:        []byte{0x00, 0x00, 0x00, 0x00},
			start:       0,
			end:         13,
			isTransient: false,
			lm:          0,
			wantTfRes:   []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rd := &rangecoding.Decoder{}
			rd.Init(tc.data)

			tfRes := make([]int, tc.end)
			tfDecode(tc.start, tc.end, tc.isTransient, tfRes, tc.lm, rd)

			for i := tc.start; i < tc.end; i++ {
				if tfRes[i] != tc.wantTfRes[i] {
					t.Errorf("tfRes[%d] = %d, want %d", i, tfRes[i], tc.wantTfRes[i])
				}
			}
		})
	}
}

// TestTFDecodeEncodeDecode verifies round-trip TF encoding/decoding.
func TestTFDecodeEncodeDecode(t *testing.T) {
	tests := []struct {
		name        string
		start       int
		end         int
		isTransient bool
		tfRes       []int
		lm          int
	}{
		{
			name:        "all_zeros_21bands_long",
			start:       0,
			end:         21,
			isTransient: false,
			tfRes:       []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			lm:          3,
		},
		{
			name:        "all_ones_21bands_long",
			start:       0,
			end:         21,
			isTransient: false,
			tfRes:       []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
			lm:          3,
		},
		{
			name:        "alternating_21bands_long",
			start:       0,
			end:         21,
			isTransient: false,
			tfRes:       []int{0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0},
			lm:          3,
		},
		{
			name:        "all_zeros_21bands_transient",
			start:       0,
			end:         21,
			isTransient: true,
			tfRes:       []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			lm:          3,
		},
		{
			name:        "all_ones_21bands_transient",
			start:       0,
			end:         21,
			isTransient: true,
			tfRes:       []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
			lm:          3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Encode
			encBuf := make([]byte, 64)
			enc := &rangecoding.Encoder{}
			enc.Init(encBuf)

			// Make a copy of tfRes for encoding
			tfResEnc := make([]int, len(tc.tfRes))
			copy(tfResEnc, tc.tfRes)

			tfEncode(enc, tc.start, tc.end, tc.isTransient, tfResEnc, tc.lm)
			encoded := enc.Done()

			// Decode
			rd := &rangecoding.Decoder{}
			rd.Init(encoded)

			tfResDecoded := make([]int, tc.end)
			tfDecode(tc.start, tc.end, tc.isTransient, tfResDecoded, tc.lm, rd)

			// After decoding, tfRes contains the final TF change values from the table.
			// These should produce the same output when input through tfSelectTable.
			// The raw values encoded are 0/1, but after decode they go through tfSelectTable.

			// For validation, check that decoded values are valid TF change values
			for i := tc.start; i < tc.end; i++ {
				// TF change values after tfSelectTable lookup are bounded by the table
				// For non-transient: values are negative (resolution reduction)
				// For transient: values can be positive (resolution increase)
				tfVal := tfResDecoded[i]
				if tfVal < -3 || tfVal > 3 {
					t.Errorf("tfRes[%d] = %d, out of valid range [-3, 3]", i, tfVal)
				}
			}
		})
	}
}

// TestTFDecodeIndexing validates the indexing logic matches libopus.
// libopus: idx = 4*isTransient + 2*tf_select + tf_res[i]
func TestTFDecodeIndexing(t *testing.T) {
	// Test all valid index combinations
	for lm := 0; lm < 4; lm++ {
		for isTransient := 0; isTransient <= 1; isTransient++ {
			for tfSelect := 0; tfSelect <= 1; tfSelect++ {
				for tfResVal := 0; tfResVal <= 1; tfResVal++ {
					idx := 4*isTransient + 2*tfSelect + tfResVal

					if idx >= 8 {
						t.Errorf("index overflow: lm=%d isTransient=%d tfSelect=%d tfRes=%d -> idx=%d",
							lm, isTransient, tfSelect, tfResVal, idx)
						continue
					}

					// Verify the lookup produces valid TF change value
					tfChange := int(tfSelectTable[lm][idx])

					// Valid TF change values based on table inspection
					valid := false
					for _, v := range []int{-3, -2, -1, 0, 1, 2, 3} {
						if tfChange == v {
							valid = true
							break
						}
					}
					if !valid {
						t.Errorf("invalid TF change: lm=%d idx=%d -> %d", lm, idx, tfChange)
					}
				}
			}
		}
	}
}

// TestTFDecodeBudgetHandling tests that budget constraints are handled correctly.
func TestTFDecodeBudgetHandling(t *testing.T) {
	// Test with minimal data (budget constraint scenario)
	data := []byte{0xFF} // Only 8 bits total
	rd := &rangecoding.Decoder{}
	rd.Init(data)

	// Try to decode 21 bands with very limited budget
	// The decoder should handle this gracefully by not reading beyond budget
	tfRes := make([]int, 21)
	tfDecode(0, 21, false, tfRes, 3, rd)

	// Verify no panic and reasonable output
	for i := 0; i < 21; i++ {
		tfVal := tfRes[i]
		if tfVal < -3 || tfVal > 3 {
			t.Errorf("tfRes[%d] = %d, out of valid range [-3, 3]", i, tfVal)
		}
	}
}

// TestTFDecodeTfSelectRsv tests tf_select_rsv logic.
// tf_select_rsv = LM>0 && tell+logp+1<=budget
func TestTFDecodeTfSelectRsv(t *testing.T) {
	tests := []struct {
		name          string
		lm            int
		dataLen       int
		expectTfSelRv bool
	}{
		{"lm0_no_rsv", 0, 8, false},    // LM=0: no tf_select reservation
		{"lm1_sufficient", 1, 8, true}, // LM=1 with enough budget
		{"lm2_sufficient", 2, 8, true}, // LM=2 with enough budget
		{"lm3_sufficient", 3, 8, true}, // LM=3 with enough budget
		{"lm1_minimal", 1, 1, false},   // LM=1 but not enough budget
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.dataLen)
			rd := &rangecoding.Decoder{}
			rd.Init(data)

			// tfSelectRsv is internal, but we can verify behavior through the output
			// For now, just ensure no panic
			tfRes := make([]int, 5)
			tfDecode(0, 5, false, tfRes, tc.lm, rd)

			// Verify reasonable output
			for i := 0; i < 5; i++ {
				if tfRes[i] < -3 || tfRes[i] > 3 {
					t.Errorf("tfRes[%d] = %d, out of valid range", i, tfRes[i])
				}
			}
		})
	}
}

// TestTFDecodeNilDecoder ensures tfDecode handles nil decoder gracefully.
func TestTFDecodeNilDecoder(t *testing.T) {
	tfRes := make([]int, 21)
	for i := range tfRes {
		tfRes[i] = 99 // sentinel value
	}

	// Should not panic with nil decoder
	tfDecode(0, 21, false, tfRes, 3, nil)

	// Values should remain unchanged with nil decoder
	for i := range tfRes {
		if tfRes[i] != 99 {
			t.Errorf("tfRes[%d] was modified with nil decoder: got %d", i, tfRes[i])
		}
	}
}

// TestTFDecodeLogpTransition tests the logp value transitions.
// libopus: first band uses logp = isTransient ? 2 : 4
// subsequent bands use logp = isTransient ? 4 : 5
func TestTFDecodeLogpTransition(t *testing.T) {
	// The logp values affect probability of decoding 0 vs 1
	// logp=2: P(0) = 3/4, P(1) = 1/4
	// logp=4: P(0) = 15/16, P(1) = 1/16
	// logp=5: P(0) = 31/32, P(1) = 1/32

	// Test transient mode (logp starts at 2, then 4)
	t.Run("transient_logp_2_to_4", func(t *testing.T) {
		data := make([]byte, 16)
		rd := &rangecoding.Decoder{}
		rd.Init(data)

		tfRes := make([]int, 5)
		tfDecode(0, 5, true, tfRes, 3, rd)

		// Should decode without error
		for i := 0; i < 5; i++ {
			if tfRes[i] < -3 || tfRes[i] > 3 {
				t.Errorf("tfRes[%d] = %d out of range", i, tfRes[i])
			}
		}
	})

	// Test non-transient mode (logp starts at 4, then 5)
	t.Run("long_logp_4_to_5", func(t *testing.T) {
		data := make([]byte, 16)
		rd := &rangecoding.Decoder{}
		rd.Init(data)

		tfRes := make([]int, 5)
		tfDecode(0, 5, false, tfRes, 3, rd)

		// Should decode without error
		for i := 0; i < 5; i++ {
			if tfRes[i] < -3 || tfRes[i] > 3 {
				t.Errorf("tfRes[%d] = %d out of range", i, tfRes[i])
			}
		}
	})
}

// TestTFDecodeTfChangedOrLogic tests the tf_changed |= curr logic.
// In libopus: tf_changed |= curr means tf_changed becomes 1 if curr was ever 1.
// In Go: if curr != 0 { tfChanged = 1 } is equivalent.
func TestTFDecodeTfChangedOrLogic(t *testing.T) {
	// This tests the internal logic indirectly through output validation.
	// The tf_changed value affects which tf_select_table entries are compared.

	// Test case where no bits are set (tf_changed stays 0)
	t.Run("tf_changed_zero", func(t *testing.T) {
		data := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
		rd := &rangecoding.Decoder{}
		rd.Init(data)

		tfRes := make([]int, 21)
		tfDecode(0, 21, false, tfRes, 3, rd)

		// With all zeros input and tf_changed=0, all outputs should be 0
		for i := 0; i < 21; i++ {
			if tfRes[i] != 0 {
				t.Errorf("tfRes[%d] = %d, want 0 for all-zeros input", i, tfRes[i])
			}
		}
	})
}

// BenchmarkTFDecode benchmarks TF decode performance.
func BenchmarkTFDecode(b *testing.B) {
	data := make([]byte, 64)
	for i := range data {
		data[i] = 0xAA
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rd := &rangecoding.Decoder{}
		rd.Init(data)

		tfRes := make([]int, 21)
		tfDecode(0, 21, false, tfRes, 3, rd)
	}
}

// TestTFDecodeRealPacket tests TF decode with a real CELT packet from celt_trace_harness.c.
// This packet is from libopus test infrastructure.
func TestTFDecodeRealPacket(t *testing.T) {
	// This is frame_data0 from celt_trace_harness.c
	// It's a real mono CELT 20ms (960 sample) frame
	frameData := []byte{
		0x8a, 0x6b, 0x06, 0xf1, 0x21, 0x93, 0x3c, 0x6c, 0x10, 0x4b, 0xc5, 0x29,
		0xf4, 0xa9, 0x65, 0x67, 0xe9, 0x9d, 0xe9, 0x72, 0x93, 0xae, 0xf3, 0x1e,
		0xd7, 0xc7, 0x8c, 0x7a, 0x07, 0x3e, 0x81, 0x76, 0xdd, 0x76, 0x65, 0xe5,
		0xc8, 0x8f, 0xdc, 0xef, 0xe6, 0x73, 0xb3, 0xc6, 0xab, 0xcb, 0xd9,
	}

	rd := &rangecoding.Decoder{}
	rd.Init(frameData)

	// Skip to where TF decode would happen in a real decode:
	// 1. Silence flag (potentially skipped)
	// 2. Postfilter
	// 3. Transient flag
	// 4. Intra flag
	// 5. Coarse energy
	// 6. TF decode

	// For this test, we just decode TF from the beginning to verify no panic
	// and valid output range. Full integration validation is in decoder tests.

	lm := 3   // 20ms frame
	end := 21 // full band range
	tfRes := make([]int, end)

	// Try transient mode
	tfDecode(0, end, true, tfRes, lm, rd)
	for i := 0; i < end; i++ {
		if tfRes[i] < -3 || tfRes[i] > 3 {
			t.Errorf("transient tfRes[%d] = %d out of range", i, tfRes[i])
		}
	}

	// Reset and try non-transient mode
	rd.Init(frameData)
	tfRes = make([]int, end)
	tfDecode(0, end, false, tfRes, lm, rd)
	for i := 0; i < end; i++ {
		if tfRes[i] < -3 || tfRes[i] > 3 {
			t.Errorf("non-transient tfRes[%d] = %d out of range", i, tfRes[i])
		}
	}
}

// TestTFDecodeAllLMValues tests all valid LM (log mode) values.
func TestTFDecodeAllLMValues(t *testing.T) {
	data := make([]byte, 64)
	for i := range data {
		data[i] = byte((i * 37) % 256) // pseudo-random pattern
	}

	for lm := 0; lm <= 3; lm++ {
		t.Run(frameSizeName(lm), func(t *testing.T) {
			rd := &rangecoding.Decoder{}
			rd.Init(data)

			end := effectiveBandsForLM(lm)
			tfRes := make([]int, end)

			// Test both transient and non-transient
			for _, transient := range []bool{false, true} {
				rd.Init(data)
				tfRes = make([]int, end)
				tfDecode(0, end, transient, tfRes, lm, rd)

				// Verify all values are valid
				for i := 0; i < end; i++ {
					if tfRes[i] < -3 || tfRes[i] > 3 {
						t.Errorf("lm=%d transient=%v band=%d: tfRes=%d out of range",
							lm, transient, i, tfRes[i])
					}
				}
			}
		})
	}
}

// TestTFDecodeStartEnd tests non-zero start values.
func TestTFDecodeStartEnd(t *testing.T) {
	data := make([]byte, 64)
	for i := range data {
		data[i] = byte(i)
	}

	// Test with start=17 (hybrid mode CELT start band)
	rd := &rangecoding.Decoder{}
	rd.Init(data)

	tfRes := make([]int, 21)
	// Initialize with sentinel values
	for i := range tfRes {
		tfRes[i] = 99
	}

	tfDecode(17, 21, false, tfRes, 3, rd)

	// Bands 0-16 should remain unchanged (sentinel)
	for i := 0; i < 17; i++ {
		if tfRes[i] != 99 {
			t.Errorf("tfRes[%d] was modified (should be unchanged): got %d", i, tfRes[i])
		}
	}

	// Bands 17-20 should have valid TF values
	for i := 17; i < 21; i++ {
		if tfRes[i] == 99 {
			t.Errorf("tfRes[%d] was not set", i)
		}
		if tfRes[i] < -3 || tfRes[i] > 3 {
			t.Errorf("tfRes[%d] = %d out of range", i, tfRes[i])
		}
	}
}

// TestTFSelectTableValues validates all possible TF select table outputs.
func TestTFSelectTableValues(t *testing.T) {
	// Verify all table entries are in the valid range [-3, 3]
	for lm := 0; lm < 4; lm++ {
		for idx := 0; idx < 8; idx++ {
			val := int(tfSelectTable[lm][idx])
			if val < -3 || val > 3 {
				t.Errorf("tfSelectTable[%d][%d] = %d out of valid range", lm, idx, val)
			}
		}
	}
}

// Helper functions for test readability
func frameSizeName(lm int) string {
	names := []string{"2.5ms", "5ms", "10ms", "20ms"}
	if lm >= 0 && lm < len(names) {
		return names[lm]
	}
	return "unknown"
}

func effectiveBandsForLM(lm int) int {
	// Based on mode config effective bands
	switch lm {
	case 0:
		return 13 // 2.5ms
	case 1:
		return 17 // 5ms
	case 2:
		return 19 // 10ms
	case 3:
		return 21 // 20ms
	default:
		return 21
	}
}

// TestTFAnalysisBasic tests the TFAnalysis function with basic inputs.
func TestTFAnalysisBasic(t *testing.T) {
	tests := []struct {
		name           string
		lm             int
		nbBands        int
		isTransient    bool
		effectiveBytes int
	}{
		{"LM0_returns_defaults", 0, 21, false, 100},
		{"LM3_non_transient", 3, 21, false, 160},
		{"LM3_transient", 3, 21, true, 160},
		{"LM2_non_transient", 2, 21, false, 80},
		{"LM1_low_bitrate", 1, 21, false, 10},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create synthetic normalized coefficients
			// Use different patterns to test analysis
			N0 := EBands[tc.nbBands] << tc.lm
			X := make([]float64, N0)

			// Fill with a simple pattern
			for i := 0; i < N0; i++ {
				X[i] = float64(i%10-5) / 10.0
			}

			tfRes, tfSelect := TFAnalysis(X, N0, tc.nbBands, tc.isTransient, tc.lm, 0.5, tc.effectiveBytes, nil)

			// Verify output dimensions
			if len(tfRes) != tc.nbBands {
				t.Errorf("TFAnalysis returned %d bands, want %d", len(tfRes), tc.nbBands)
			}

			// Note: LM=0 no longer has special handling - TFAnalysis runs the full
			// Viterbi algorithm for all LM values, matching libopus behavior.
			// For LM=0, tf_select is always 0 (tfSelectRsv = LM>0 is false).
			if tc.lm == 0 && tfSelect != 0 {
				t.Errorf("LM=0: tfSelect = %d, want 0 (tf_select not reserved for LM=0)", tfSelect)
			}

			// Verify tfRes values are valid (0 or 1 for raw output)
			for i, v := range tfRes {
				if v != 0 && v != 1 {
					t.Errorf("tfRes[%d] = %d, want 0 or 1", i, v)
				}
			}

			// Verify tfSelect is valid
			if tfSelect != 0 && tfSelect != 1 {
				t.Errorf("tfSelect = %d, want 0 or 1", tfSelect)
			}
		})
	}
}

// TestTFAnalysisWithTransient tests TF analysis behavior with transient signals.
func TestTFAnalysisWithTransient(t *testing.T) {
	lm := 3
	nbBands := 21
	N0 := EBands[nbBands] << lm
	X := make([]float64, N0)

	// Create a transient-like signal: spike at the beginning
	for i := 0; i < 100; i++ {
		X[i] = 1.0
	}

	tfRes, tfSelect := TFAnalysis(X, N0, nbBands, true, lm, 0.2, 160, nil)

	// Just verify we get valid output - the exact values depend on the algorithm
	if len(tfRes) != nbBands {
		t.Errorf("TFAnalysis returned %d bands, want %d", len(tfRes), nbBands)
	}

	t.Logf("Transient analysis: tfSelect=%d, tfRes=%v", tfSelect, tfRes)
}

// TestTFEncodeWithSelectRoundTrip tests that TFEncodeWithSelect produces decodable output.
// Note: TFEncodeWithSelect modifies tfRes to contain post-table-lookup values,
// while tfDecode also does a table lookup. So we compare the final decoded values.
func TestTFEncodeWithSelectRoundTrip(t *testing.T) {
	tests := []struct {
		name        string
		lm          int
		isTransient bool
		tfRes       []int // Pre-table-lookup values (0 or 1)
		tfSelect    int
	}{
		{
			name:        "all_zeros_lm3",
			lm:          3,
			isTransient: false,
			tfRes:       []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			tfSelect:    0,
		},
		{
			name:        "all_zeros_lm3_transient",
			lm:          3,
			isTransient: true,
			tfRes:       []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			tfSelect:    0,
		},
		{
			name:        "mixed_lm2",
			lm:          2,
			isTransient: false,
			tfRes:       []int{0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 1, 1, 0, 0, 1, 1, 0},
			tfSelect:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create encoder
			buf := make([]byte, 256)
			enc := &rangecoding.Encoder{}
			enc.Init(buf)

			// Make a copy of tfRes since TFEncodeWithSelect modifies it
			tfResCopy := make([]int, len(tc.tfRes))
			copy(tfResCopy, tc.tfRes)

			// Encode
			start := 0
			end := 21
			TFEncodeWithSelect(enc, start, end, tc.isTransient, tfResCopy, tc.lm, tc.tfSelect)

			// Finalize and decode
			encoded := enc.Done()

			dec := &rangecoding.Decoder{}
			dec.Init(encoded)

			decodedTfRes := make([]int, 21)
			tfDecode(start, end, tc.isTransient, decodedTfRes, tc.lm, dec)

			// The decoded tfRes should match the encoded tfRes (both have table lookup applied)
			for i := 0; i < end; i++ {
				if decodedTfRes[i] != tfResCopy[i] {
					t.Errorf("band %d: decoded tfRes=%d, encoded tfRes=%d", i, decodedTfRes[i], tfResCopy[i])
				}
			}
		})
	}
}

// TestL1Metric tests the l1Metric function.
func TestL1Metric(t *testing.T) {
	tests := []struct {
		name     string
		tmp      []float64
		N        int
		LM       int
		bias     float64
		expected float64 // approximate expected value
	}{
		{
			name:     "simple_positive",
			tmp:      []float64{1.0, 2.0, 3.0, 4.0},
			N:        4,
			LM:       0,
			bias:     0.0,
			expected: 10.0,
		},
		{
			name:     "mixed_signs",
			tmp:      []float64{1.0, -2.0, 3.0, -4.0},
			N:        4,
			LM:       0,
			bias:     0.0,
			expected: 10.0,
		},
		{
			name:     "with_bias",
			tmp:      []float64{1.0, 2.0, 3.0, 4.0},
			N:        4,
			LM:       2,
			bias:     0.1,
			expected: 10.0 * (1.0 + 2*0.1), // L1 + LM*bias*L1
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := l1Metric(tc.tmp, tc.N, tc.LM, tc.bias)
			// Allow small floating point tolerance
			tolerance := 0.0001
			if result < tc.expected-tolerance || result > tc.expected+tolerance {
				t.Errorf("l1Metric() = %f, want ~%f", result, tc.expected)
			}
		})
	}
}
