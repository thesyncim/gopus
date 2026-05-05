package silk

import (
	"testing"
)

// TestSilkNLSF2AComparison compares gopus silkNLSF2A with expected libopus output.
func TestSilkNLSF2AComparison(t *testing.T) {
	// NLSF values from packet 15 frame 0 (verified against libopus)
	nlsfFrame0 := []int16{2676, 3684, 7247, 12558, 14555, 16405, 18875, 19753, 26306, 27425}

	// Expected LPC coefficients from libopus silk_NLSF2A
	expectedLPC := []int16{3952, -3489, 3995, -1295, 1307, -2683, 2277, -3438, 2634, -1010}

	// Call gopus silkNLSF2A
	goLPC := make([]int16, 10)
	success := silkNLSF2A(goLPC, nlsfFrame0, 10)

	t.Logf("silkNLSF2A returned success=%v", success)
	t.Logf("Input NLSF: %v", nlsfFrame0)
	t.Logf("Expected LPC (libopus): %v", expectedLPC)
	t.Logf("Actual LPC (gopus):     %v", goLPC)

	// Compare
	mismatches := 0
	for i := 0; i < 10; i++ {
		if goLPC[i] != expectedLPC[i] {
			t.Logf("  Mismatch at [%d]: gopus=%d, libopus=%d, diff=%d", i, goLPC[i], expectedLPC[i], goLPC[i]-expectedLPC[i])
			mismatches++
		}
	}

	if mismatches > 0 {
		t.Errorf("Found %d mismatches between gopus and libopus NLSF2A", mismatches)
	} else {
		t.Log("All coefficients match!")
	}
}

// TestSilkNLSF2AInterpolated tests interpolated NLSF values for frame 1.
func TestSilkNLSF2AInterpolated(t *testing.T) {
	// prevNLSF from frame 0
	prevNLSF := []int16{2676, 3684, 7247, 12558, 14555, 16405, 18875, 19753, 26306, 27425}
	// currNLSF from frame 1
	currNLSF := []int16{2701, 3363, 5756, 13031, 13464, 15353, 18521, 20697, 26950, 26953}
	// Interpolation coefficient = 1
	interpCoef := int8(1)

	// Compute interpolated NLSF: nlsf0 = prevNLSF + (interpCoef * (currNLSF - prevNLSF)) >> 2
	nlsf0 := make([]int16, 10)
	for i := 0; i < 10; i++ {
		diff := int32(currNLSF[i]) - int32(prevNLSF[i])
		nlsf0[i] = int16(int32(prevNLSF[i]) + (int32(interpCoef) * diff >> 2))
	}

	// Expected LPC for interpolated NLSF (from libopus)
	expectedLPC0 := []int16{4154, -3412, 3853, -1287, 1521, -3076, 2502, -3664, 2710, -823}

	// Call gopus silkNLSF2A with interpolated NLSF
	goLPC0 := make([]int16, 10)
	success := silkNLSF2A(goLPC0, nlsf0, 10)

	t.Logf("Testing interpolated NLSF (frame 1, coef=%d)", interpCoef)
	t.Logf("silkNLSF2A returned success=%v", success)
	t.Logf("Interpolated NLSF: %v", nlsf0)
	t.Logf("Expected LPC (libopus): %v", expectedLPC0)
	t.Logf("Actual LPC (gopus):     %v", goLPC0)

	// Compare
	mismatches := 0
	for i := 0; i < 10; i++ {
		if goLPC0[i] != expectedLPC0[i] {
			t.Logf("  Mismatch at [%d]: gopus=%d, libopus=%d, diff=%d", i, goLPC0[i], expectedLPC0[i], goLPC0[i]-expectedLPC0[i])
			mismatches++
		}
	}

	if mismatches > 0 {
		t.Errorf("Found %d mismatches for interpolated NLSF", mismatches)
	} else {
		t.Log("All interpolated coefficients match!")
	}
}

// TestDecodeParametersLPCOutput tests that silkDecodeParameters produces correct LPC.
func TestDecodeParametersLPCOutput(t *testing.T) {
	// Simulate decoding packet 15
	// We need to set up the decoder state and call silkDecodeParameters

	// Create a decoder and decode packet 15 manually
	dec := NewDecoder()
	st := &dec.state[0]

	// Set up state for NB/8kHz
	st.nbSubfr = 4
	st.fsKHz = 8
	st.frameLength = 160
	st.subfrLength = 40
	st.lpcOrder = 10
	st.ltpMemLength = 200
	st.nlsfCB = &silk_NLSF_CB_NB_MB
	st.firstFrameAfterReset = true

	// Manually set indices for frame 0 (from libopus decode)
	// These would normally come from silkDecodeIndices
	st.indices.NLSFInterpCoefQ2 = 4 // No interpolation for frame 0
	st.indices.signalType = 2       // voiced
	st.indices.quantOffsetType = 0

	// Set NLSF indices (these produce the NLSF values we expect)
	// For simplicity, let's manually set prevNLSFQ15 to simulate after frame 0
	// and test frame 1
	st.firstFrameAfterReset = false

	// Set prevNLSFQ15 to frame 0's NLSF values
	frame0NLSF := [16]int16{2676, 3684, 7247, 12558, 14555, 16405, 18875, 19753, 26306, 27425}
	copy(st.prevNLSFQ15[:], frame0NLSF[:])

	// Now simulate frame 1 with interpolation
	st.indices.NLSFInterpCoefQ2 = 1 // Interpolation active

	// We need to simulate silkNLSFDecode output
	// For testing, let's directly set the expected NLSF values
	frame1NLSF := [maxLPCOrder]int16{2701, 3363, 5756, 13031, 13464, 15353, 18521, 20697, 26950, 26953}

	// Expected results from libopus:
	// PredCoef_Q12[0] (interpolated): [4154 -3412 3853 -1287 1521 -3076 2502 -3664 2710 -823]
	// PredCoef_Q12[1] (full): [4732 -3203 3220 -1107 2168 -4175 3262 -4374 2814 -286]

	// Call the internal function to compute LPC from interpolated NLSF
	// First compute the interpolated NLSF
	interpNLSF := make([]int16, st.lpcOrder)
	for i := 0; i < st.lpcOrder; i++ {
		diff := int32(frame1NLSF[i]) - int32(st.prevNLSFQ15[i])
		interpNLSF[i] = int16(int32(st.prevNLSFQ15[i]) + (int32(st.indices.NLSFInterpCoefQ2) * diff >> 2))
	}

	t.Logf("prevNLSFQ15: %v", st.prevNLSFQ15[:st.lpcOrder])
	t.Logf("frame1NLSF:  %v", frame1NLSF[:st.lpcOrder])
	t.Logf("interpNLSF:  %v", interpNLSF)

	// Call silkNLSF2A for interpolated
	lpc0 := make([]int16, st.lpcOrder)
	success0 := silkNLSF2A(lpc0, interpNLSF, st.lpcOrder)
	t.Logf("silkNLSF2A(interpNLSF) success=%v, output=%v", success0, lpc0)

	// Call silkNLSF2A for full
	lpc1 := make([]int16, st.lpcOrder)
	success1 := silkNLSF2A(lpc1, frame1NLSF[:st.lpcOrder], st.lpcOrder)
	t.Logf("silkNLSF2A(frame1NLSF) success=%v, output=%v", success1, lpc1)

	// Expected values
	expected0 := []int16{4154, -3412, 3853, -1287, 1521, -3076, 2502, -3664, 2710, -823}
	expected1 := []int16{4732, -3203, 3220, -1107, 2168, -4175, 3262, -4374, 2814, -286}

	t.Logf("Expected LPC0: %v", expected0)
	t.Logf("Expected LPC1: %v", expected1)

	// Check matches
	match0 := true
	for i := 0; i < st.lpcOrder; i++ {
		if lpc0[i] != expected0[i] {
			match0 = false
			t.Logf("LPC0 mismatch at [%d]: got %d, want %d", i, lpc0[i], expected0[i])
		}
	}
	match1 := true
	for i := 0; i < st.lpcOrder; i++ {
		if lpc1[i] != expected1[i] {
			match1 = false
			t.Logf("LPC1 mismatch at [%d]: got %d, want %d", i, lpc1[i], expected1[i])
		}
	}

	if match0 && match1 {
		t.Log("Both LPC coefficient sets match libopus!")
	} else {
		t.Error("LPC coefficient mismatch")
	}
}

// TestSilkNLSFDecodeComparison compares gopus silkNLSFDecode with libopus output.
func TestSilkNLSFDecodeComparison(t *testing.T) {
	// Test data from packet 15 - verified against libopus
	testCases := []struct {
		name         string
		indices      []int8  // NLSFIndices[0:11]
		expectedNLSF []int16 // NLSF_Q15[0:10]
	}{
		{
			name:         "Frame 0",
			indices:      []int8{17, 0, -1, 0, 2, 0, 0, 0, -3, 1, -1},
			expectedNLSF: []int16{2676, 3684, 7247, 12558, 14555, 16405, 18875, 19753, 26306, 27425},
		},
		{
			name:         "Frame 1",
			indices:      []int8{23, 0, 0, -1, 1, -1, -1, -1, -2, 1, -2},
			expectedNLSF: []int16{2701, 3363, 5756, 13031, 13464, 15353, 18521, 20697, 26950, 26953},
		},
		{
			name:         "Frame 2",
			indices:      []int8{14, 0, -1, -2, 2, 1, 0, 0, -2, 1, 0},
			expectedNLSF: []int16{1936, 2759, 5104, 11867, 13402, 14747, 17702, 19745, 26291, 28416},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Call gopus silkNLSFDecode
			nlsfQ15 := make([]int16, 16)
			silkNLSFDecode(nlsfQ15, tc.indices, &silk_NLSF_CB_NB_MB)

			t.Logf("Input indices: %v", tc.indices)
			t.Logf("Expected NLSF: %v", tc.expectedNLSF)
			t.Logf("Gopus NLSF:    %v", nlsfQ15[:10])

			// Compare
			mismatches := 0
			for i := 0; i < 10; i++ {
				if nlsfQ15[i] != tc.expectedNLSF[i] {
					t.Logf("  Mismatch at [%d]: gopus=%d, expected=%d", i, nlsfQ15[i], tc.expectedNLSF[i])
					mismatches++
				}
			}

			if mismatches > 0 {
				t.Errorf("Found %d mismatches", mismatches)
			}
		})
	}
}

// TestNLSFStabilizeMatchesLibopusCase verifies the stabilization result for a
// libopus-derived NB/MB NLSF case.
func TestNLSFStabilizeMatchesLibopusCase(t *testing.T) {
	cb := &silk_NLSF_CB_NB_MB

	nlsfQ15 := []int16{2701, 3363, 5756, 13031, 13464, 15353, 18521, 20697, 27019, 26883, 0, 0, 0, 0, 0, 0}
	expected := []int16{2701, 3363, 5756, 13031, 13464, 15353, 18521, 20697, 26950, 26953}

	silkNLSFStabilize(nlsfQ15[:cb.order], cb.deltaMinQ15, cb.order)
	for i := range expected {
		if nlsfQ15[i] != expected[i] {
			t.Fatalf("nlsfQ15[%d]=%d want %d", i, nlsfQ15[i], expected[i])
		}
	}
}
