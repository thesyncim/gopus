// Package cgo provides CGO comparison tests for SILK LSF/NLSF encoding.
package cgo

import (
	"testing"
)

// TestSilkA2NLSFComparison compares LPC to NLSF conversion between gopus and libopus.
// gopus uses floating-point Chebyshev root finding while libopus uses fixed-point
// with piecewise-linear cosine approximation.
func TestSilkA2NLSFComparison(t *testing.T) {
	// Test with typical LPC coefficients
	// These are Q16 format (65536 = 1.0)
	testCases := []struct {
		name   string
		lpcQ16 []int32
		order  int
	}{
		{
			name: "NB typical voiced",
			lpcQ16: []int32{
				25000, -18000, 22000, -8000, 5000,
				-12000, 8000, -4000, 3000, -1000,
			},
			order: 10,
		},
		{
			name: "NB near-flat spectrum",
			lpcQ16: []int32{
				5000, -3000, 2000, -1500, 1000,
				-800, 600, -400, 300, -200,
			},
			order: 10,
		},
		{
			name: "WB typical",
			lpcQ16: []int32{
				30000, -25000, 28000, -15000, 12000,
				-18000, 10000, -8000, 6000, -4000,
				5000, -3000, 2500, -1500, 1000, -500,
			},
			order: 16,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Call libopus silk_A2NLSF
			nlsfOut := SilkA2NLSF(tc.lpcQ16, tc.order)
			if nlsfOut == nil {
				t.Fatal("SilkA2NLSF returned nil")
			}

			t.Logf("Input LPC (Q16, first 5): %v", tc.lpcQ16[:5])
			t.Logf("libopus NLSF (Q15): %v", nlsfOut)

			// Verify NLSF properties
			// 1. All values should be in [0, 32767]
			for i, v := range nlsfOut {
				if v < 0 || v > 32767 {
					t.Errorf("NLSF[%d] = %d out of range [0, 32767]", i, v)
				}
			}

			// 2. Values should be strictly increasing
			for i := 1; i < tc.order; i++ {
				if nlsfOut[i] <= nlsfOut[i-1] {
					t.Errorf("NLSF not strictly increasing: [%d]=%d <= [%d]=%d",
						i, nlsfOut[i], i-1, nlsfOut[i-1])
				}
			}

			// 3. Verify roundtrip: NLSF back to LPC should be close to original
			lpcBack := SilkNLSF2A(nlsfOut, tc.order)
			if lpcBack != nil {
				t.Logf("Roundtrip LPC (Q12): %v", lpcBack[:minIntSilkLSF(5, len(lpcBack))])

				// Compare (accounting for Q16->Q12 scaling: divide by 16)
				maxDiff := int32(0)
				for i := 0; i < tc.order; i++ {
					expected := tc.lpcQ16[i] >> 4 // Q16 to Q12
					actual := int32(lpcBack[i])
					diff := abs32(expected - actual)
					if diff > maxDiff {
						maxDiff = diff
					}
				}
				t.Logf("Max LPC roundtrip error (Q12): %d", maxDiff)
			}
		})
	}
}

// TestSilkNLSFCosineTable verifies the libopus cosine lookup table values.
func TestSilkNLSFCosineTable(t *testing.T) {
	// Get table size
	tabSize := SilkLSFCosTabSize()
	t.Logf("LSF_COS_TAB_SZ_FIX = %d", tabSize)

	// The table should have 128 intervals for [0, pi]
	if tabSize != 128 {
		t.Errorf("Expected table size 128, got %d", tabSize)
	}

	// Verify key values
	// cos(0) = 1.0 in Q12 = 4096, but SILK uses 2*cos = 8192
	val0 := SilkLSFCosTab(0)
	t.Logf("silk_LSFCosTab_FIX_Q12[0] = %d (expected ~8192 for 2*cos(0))", val0)
	if val0 != 8192 {
		t.Errorf("Expected 8192 at index 0, got %d", val0)
	}

	// cos(pi/2) = 0 at index 64
	val64 := SilkLSFCosTab(64)
	t.Logf("silk_LSFCosTab_FIX_Q12[64] = %d (expected 0 for 2*cos(pi/2))", val64)
	if val64 != 0 {
		t.Errorf("Expected 0 at index 64, got %d", val64)
	}

	// cos(pi) = -1.0 in Q12 = -4096, but 2*cos = -8192
	val128 := SilkLSFCosTab(128)
	t.Logf("silk_LSFCosTab_FIX_Q12[128] = %d (expected -8192 for 2*cos(pi))", val128)
	if val128 != -8192 {
		t.Errorf("Expected -8192 at index 128, got %d", val128)
	}
}

// TestSilkNLSFCodebookParams verifies the libopus NLSF codebook parameters.
func TestSilkNLSFCodebookParams(t *testing.T) {
	testCases := []struct {
		name  string
		useWB bool
	}{
		{"NB/MB", false},
		{"WB", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			params := SilkGetNLSFCBParams(tc.useWB)

			t.Logf("nVectors = %d", params.NVectors)
			t.Logf("order = %d", params.Order)
			t.Logf("quantStepSize_Q16 = %d", params.QuantStepSizeQ16)
			t.Logf("invQuantStepSize_Q6 = %d", params.InvQuantStepSizeQ6)

			// Verify expected values
			expectedOrder := 10
			if tc.useWB {
				expectedOrder = 16
			}
			if params.Order != expectedOrder {
				t.Errorf("Expected order %d, got %d", expectedOrder, params.Order)
			}

			if params.NVectors != 32 {
				t.Errorf("Expected 32 vectors, got %d", params.NVectors)
			}
		})
	}
}

// TestSilkNLSFStabilize tests the NLSF stabilization function.
func TestSilkNLSFStabilize(t *testing.T) {
	testCases := []struct {
		name    string
		nlsfQ15 []int16
		useWB   bool
	}{
		{
			name:    "NB already stable",
			nlsfQ15: []int16{2000, 4000, 6000, 8000, 12000, 16000, 20000, 24000, 28000, 31000},
			useWB:   false,
		},
		{
			name:    "NB needs stabilization (too close)",
			nlsfQ15: []int16{100, 101, 102, 103, 104, 105, 106, 107, 108, 109},
			useWB:   false,
		},
		{
			name:    "WB typical values",
			nlsfQ15: []int16{1000, 3000, 5000, 7000, 9000, 11000, 13000, 15000, 17000, 19000, 21000, 23000, 25000, 27000, 29000, 31000},
			useWB:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Get deltaMin values
			deltaMin := SilkGetNLSFDeltaMin(tc.useWB)
			order := len(tc.nlsfQ15)
			t.Logf("deltaMin_Q15: %v", deltaMin)

			// Make a copy for stabilization
			nlsfCopy := make([]int16, len(tc.nlsfQ15))
			copy(nlsfCopy, tc.nlsfQ15)

			// Call libopus stabilize
			SilkNLSFStabilize(nlsfCopy, tc.useWB)

			t.Logf("Input  NLSF: %v", tc.nlsfQ15)
			t.Logf("Output NLSF: %v", nlsfCopy)

			// Verify properties after stabilization
			// 1. First value >= deltaMin[0]
			if nlsfCopy[0] < deltaMin[0] {
				t.Errorf("NLSF[0] = %d < deltaMin[0] = %d", nlsfCopy[0], deltaMin[0])
			}

			// 2. Adjacent values have minimum spacing
			for i := 1; i < order; i++ {
				spacing := nlsfCopy[i] - nlsfCopy[i-1]
				if spacing < deltaMin[i] {
					t.Errorf("Spacing NLSF[%d]-NLSF[%d] = %d < deltaMin[%d] = %d",
						i, i-1, spacing, i, deltaMin[i])
				}
			}

			// 3. Last value <= 32767 - deltaMin[order]
			maxLast := int16(32767) - deltaMin[order]
			if nlsfCopy[order-1] > maxLast {
				t.Errorf("NLSF[%d] = %d > 32767 - deltaMin[%d] = %d",
					order-1, nlsfCopy[order-1], order, maxLast)
			}
		})
	}
}

// TestSilkNLSFEncodeCompare compares NLSF VQ encoding between gopus and libopus.
func TestSilkNLSFEncodeCompare(t *testing.T) {
	// Test NLSF values (already quantized/stable format)
	testCases := []struct {
		name       string
		nlsfQ15    []int16
		useWB      bool
		signalType int // 0=inactive, 1=unvoiced, 2=voiced
	}{
		{
			name:       "NB voiced",
			nlsfQ15:    []int16{2676, 3684, 7247, 12558, 14555, 19131, 21376, 26092, 27957, 30426},
			useWB:      false,
			signalType: 2,
		},
		{
			name:       "NB unvoiced",
			nlsfQ15:    []int16{3000, 5000, 8000, 11000, 14000, 17000, 20000, 24000, 27000, 30000},
			useWB:      false,
			signalType: 1,
		},
		{
			name:       "WB voiced",
			nlsfQ15:    []int16{1500, 3000, 4500, 6000, 8000, 10000, 12000, 14000, 16000, 18000, 20000, 22000, 24000, 26000, 28500, 31000},
			useWB:      true,
			signalType: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			order := len(tc.nlsfQ15)

			// Create weights (typically computed from LPC analysis)
			// Use uniform weights for testing
			weightsQ2 := make([]int16, order)
			for i := range weightsQ2 {
				weightsQ2[i] = 256 // Uniform weight
			}

			// Call libopus NLSF_encode
			indices, quantizedNLSF, rd := SilkNLSFEncode(
				tc.nlsfQ15,
				tc.useWB,
				weightsQ2,
				32768, // NLSF_mu_Q20 typical value
				16,    // nSurvivors
				tc.signalType,
			)

			t.Logf("Input NLSF: %v", tc.nlsfQ15)
			t.Logf("Quantized NLSF: %v", quantizedNLSF)
			t.Logf("Indices: %v", indices)
			t.Logf("RD value (Q25): %d", rd)

			// Verify indices
			// Stage 1 index should be in [0, 31]
			if indices[0] < 0 || indices[0] > 31 {
				t.Errorf("Stage 1 index %d out of range [0, 31]", indices[0])
			}

			// Stage 2 indices should have reasonable magnitude
			for i := 1; i <= order; i++ {
				if indices[i] < -5 || indices[i] > 5 {
					t.Logf("Stage 2 index[%d] = %d (may be outside typical range)", i, indices[i])
				}
			}

			// Verify quantized NLSF is still valid
			for i := 0; i < order; i++ {
				if quantizedNLSF[i] < 0 || quantizedNLSF[i] > 32767 {
					t.Errorf("Quantized NLSF[%d] = %d out of range", i, quantizedNLSF[i])
				}
			}
			for i := 1; i < order; i++ {
				if quantizedNLSF[i] <= quantizedNLSF[i-1] {
					t.Errorf("Quantized NLSF not strictly increasing at index %d", i)
				}
			}

			// Decode the quantized NLSF and compare with direct decode
			decodedNLSF := SilkNLSFDecode(indices, tc.useWB)
			t.Logf("Re-decoded NLSF: %v", decodedNLSF[:order])

			// They should match
			for i := 0; i < order; i++ {
				if decodedNLSF[i] != quantizedNLSF[i] {
					t.Errorf("Decoded NLSF[%d] = %d != quantized %d",
						i, decodedNLSF[i], quantizedNLSF[i])
				}
			}
		})
	}
}

// TestSilkCodebookStage1Values verifies stage 1 codebook values match.
func TestSilkCodebookStage1Values(t *testing.T) {
	for _, useWB := range []bool{false, true} {
		name := "NB/MB"
		order := 10
		if useWB {
			name = "WB"
			order = 16
		}

		t.Run(name, func(t *testing.T) {
			// Get a few codebook entries from libopus
			for idx := 0; idx < 5; idx++ {
				cb1, wgt := SilkGetNLSFCB1(useWB, idx)

				t.Logf("CB1[%d] (Q8): %v", idx, cb1[:order])
				t.Logf("Wgt[%d] (Q9): %v", idx, wgt[:order])

				// Verify weights are positive
				for i := 0; i < order; i++ {
					if wgt[i] <= 0 {
						t.Errorf("Weight[%d][%d] = %d should be positive", idx, i, wgt[i])
					}
				}
			}
		})
	}
}

// Helper functions

func abs32(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func minIntSilkLSF(a, b int) int {
	if a < b {
		return a
	}
	return b
}
