//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
)

// TestPacket15NLSFCompare compares NLSF state between gopus and libopus for packet 15.
// This is the key test for diagnosing the divergence in frame 1 which has interpFlag=true.
func TestPacket15NLSFCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil || len(packets) < 16 {
		t.Skip("Could not load packets")
	}

	pkt := packets[15]
	toc := gopus.ParseTOC(pkt[0])

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	config := silk.GetBandwidthConfig(silkBW)

	fsKHz := config.SampleRate / 1000
	nbSubfr := 4
	framesPerPacket := 3
	lpcOrder := 10 // NB/MB uses order 10

	t.Logf("Packet 15: fsKHz=%d, lpcOrder=%d, frames=%d", fsKHz, lpcOrder, framesPerPacket)

	// Get NLSF state from libopus
	libStates, err := SilkDecodeNLSFState(pkt[1:], fsKHz, nbSubfr, framesPerPacket, framesPerPacket, lpcOrder)
	if err != nil || libStates == nil {
		t.Fatal("Could not decode NLSF state from libopus")
	}

	t.Log("\n=== libopus NLSF state per frame ===")
	for i, st := range libStates {
		t.Logf("Frame %d: NLSFInterpCoefQ2=%d (interpFlag=%v)", i, st.NLSFInterpCoefQ2, st.NLSFInterpCoefQ2 < 4)
		t.Logf("  prevNLSF_Q15 (first 5): %v", st.PrevNLSFQ15[:5])
		t.Logf("  currNLSF_Q15 (first 5): %v", st.CurrNLSFQ15[:5])
		t.Logf("  PredCoef_Q12[0] (first 5): %v", st.PredCoef0Q12[:5])
		t.Logf("  PredCoef_Q12[1] (first 5): %v", st.PredCoef1Q12[:5])
	}

	// Now decode with gopus and compare
	t.Log("\n=== gopus NLSF state comparison ===")

	// Decode frame by frame to compare state
	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()

	// We need to access internal state - let's use the Debug functions
	duration := silk.FrameDurationFromTOC(toc.FrameSize)
	_, err = goDec.DecodeFrame(&rd, silkBW, duration, true)
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	// Get the final NLSF state from gopus
	goState := goDec.GetDecoderState()
	if goState != nil {
		t.Log("\n=== gopus final state ===")
		t.Logf("prevNLSF_Q15 (first 5): %v", goState.PrevNLSFQ15[:5])
	}

	// Compare PredCoef_Q12 for each frame
	// The key insight: frame 1 has interpFlag=true, so PredCoef_Q12[0] should differ from [1]
	t.Log("\n=== PredCoef_Q12 comparison ===")
	for i, st := range libStates {
		if st.NLSFInterpCoefQ2 < 4 {
			t.Logf("Frame %d: INTERPOLATION ACTIVE (coef=%d)", i, st.NLSFInterpCoefQ2)
			// Check if PredCoef[0] != PredCoef[1]
			same := true
			for j := 0; j < lpcOrder; j++ {
				if st.PredCoef0Q12[j] != st.PredCoef1Q12[j] {
					same = false
					break
				}
			}
			if !same {
				t.Log("  PredCoef[0] DIFFERS from PredCoef[1] as expected")
			} else {
				t.Log("  WARNING: PredCoef[0] == PredCoef[1] despite interp")
			}
		} else {
			t.Logf("Frame %d: No interpolation (coef=%d)", i, st.NLSFInterpCoefQ2)
		}
	}
}

// TestNLSFInterpolationFormula tests the NLSF interpolation formula directly.
func TestNLSFInterpolationFormula(t *testing.T) {
	// Test the interpolation formula:
	// nlsf0[i] = prevNLSF[i] + (NLSFInterpCoefQ2 * (currNLSF[i] - prevNLSF[i])) >> 2

	testCases := []struct {
		prevNLSF      int16
		currNLSF      int16
		interpCoef    int8
		expectedNLSF0 int16
	}{
		// When interpCoef=1: nlsf0 = prevNLSF + 0.25 * (currNLSF - prevNLSF) = 0.75*prev + 0.25*curr
		{1000, 2000, 1, 1250},
		{2000, 1000, 1, 1750},
		{0, 4000, 1, 1000},

		// When interpCoef=2: nlsf0 = prevNLSF + 0.5 * (currNLSF - prevNLSF) = 0.5*prev + 0.5*curr
		{1000, 2000, 2, 1500},
		{2000, 1000, 2, 1500},

		// When interpCoef=3: nlsf0 = prevNLSF + 0.75 * (currNLSF - prevNLSF) = 0.25*prev + 0.75*curr
		{1000, 2000, 3, 1750},
		{2000, 1000, 3, 1250},

		// When interpCoef=4: no interpolation, nlsf0 = currNLSF (but code path skipped)
		// When interpCoef=0: nlsf0 = prevNLSF (weird edge case)
		{1000, 2000, 0, 1000},
	}

	for _, tc := range testCases {
		diff := int32(tc.currNLSF) - int32(tc.prevNLSF)
		nlsf0 := int16(int32(tc.prevNLSF) + (int32(tc.interpCoef) * diff >> 2))
		if nlsf0 != tc.expectedNLSF0 {
			t.Errorf("Interp(%d, %d, coef=%d): got %d, want %d",
				tc.prevNLSF, tc.currNLSF, tc.interpCoef, nlsf0, tc.expectedNLSF0)
		}
	}
	t.Log("All NLSF interpolation formula tests passed")
}

// TestComparePacket4And15Interpolation compares interpolation behavior between packets 4 and 15.
func TestComparePacket4And15Interpolation(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil {
		t.Skip("Could not load packets")
	}

	packetsToCheck := []int{4, 15}
	for _, pktIdx := range packetsToCheck {
		if pktIdx >= len(packets) {
			continue
		}
		pkt := packets[pktIdx]
		toc := gopus.ParseTOC(pkt[0])
		if toc.Mode != gopus.ModeSILK {
			continue
		}

		silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
		if !ok {
			continue
		}
		config := silk.GetBandwidthConfig(silkBW)
		fsKHz := config.SampleRate / 1000
		nbSubfr := 4
		framesPerPacket := 3
		lpcOrder := 10

		t.Logf("\n============ Packet %d ============", pktIdx)

		libStates, err := SilkDecodeNLSFState(pkt[1:], fsKHz, nbSubfr, framesPerPacket, framesPerPacket, lpcOrder)
		if err != nil || libStates == nil {
			t.Logf("Could not decode NLSF state")
			continue
		}

		for i, st := range libStates {
			interpActive := st.NLSFInterpCoefQ2 < 4
			t.Logf("Frame %d: NLSFInterpCoef=%d (interp=%v)", i, st.NLSFInterpCoefQ2, interpActive)

			if interpActive && i > 0 {
				// This frame uses interpolation - compare prevNLSF with previous frame's currNLSF
				prevFrame := libStates[i-1]
				t.Log("  Interpolation details:")
				t.Logf("    prevNLSF (from frame %d): %v", i-1, prevFrame.CurrNLSFQ15[:5])
				t.Logf("    currNLSF (this frame):    %v", st.CurrNLSFQ15[:5])

				// Compute expected interpolated NLSF
				expectedNLSF0 := make([]int16, lpcOrder)
				for j := 0; j < lpcOrder; j++ {
					diff := int32(st.CurrNLSFQ15[j]) - int32(prevFrame.CurrNLSFQ15[j])
					expectedNLSF0[j] = int16(int32(prevFrame.CurrNLSFQ15[j]) + (int32(st.NLSFInterpCoefQ2) * diff >> 2))
				}
				t.Logf("    expected nlsf0 (first 5): %v", expectedNLSF0[:5])
			}
		}
	}
}

// TestPacket15LPCCoefCompare compares the actual LPC coefficients used during decode.
func TestPacket15LPCCoefCompare(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil || len(packets) < 16 {
		t.Skip("Could not load packets")
	}

	pkt := packets[15]
	toc := gopus.ParseTOC(pkt[0])

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	config := silk.GetBandwidthConfig(silkBW)

	fsKHz := config.SampleRate / 1000
	nbSubfr := 4
	framesPerPacket := 3
	lpcOrder := 10

	// Get libopus LPC state
	libStates, err := SilkDecodeNLSFState(pkt[1:], fsKHz, nbSubfr, framesPerPacket, framesPerPacket, lpcOrder)
	if err != nil || libStates == nil {
		t.Fatal("Could not decode NLSF state from libopus")
	}

	t.Log("=== Frame 1 LPC Coefficient Comparison (interpolation active) ===")
	libFrame1 := libStates[1]

	t.Log("libopus PredCoef_Q12[0] (interpolated, for subframes 0-1):")
	t.Logf("  %v", libFrame1.PredCoef0Q12)

	t.Log("libopus PredCoef_Q12[1] (full, for subframes 2-3):")
	t.Logf("  %v", libFrame1.PredCoef1Q12)

	// Now decode with gopus and extract state per frame using trace callback
	t.Log("\n=== gopus per-subframe trace ===")

	var rd rangecoding.Decoder
	rd.Init(pkt[1:])
	goDec := silk.NewDecoder()

	duration := silk.FrameDurationFromTOC(toc.FrameSize)

	// Use tracing decode
	_, err = goDec.DecodeFrameWithTrace(&rd, silkBW, duration, true, func(frame, k int, info silk.TraceInfo) {
		if frame == 1 && (k == 0 || k == 2) {
			t.Logf("gopus frame=%d subframe=%d: A_Q12[0:5]=%v", frame, k, info.A_Q12[:5])
		}
	})
	if err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	// Compare first 5 coefficients for frame 1
	t.Log("\n=== Coefficient comparison for frame 1 ===")
	t.Log("For subframes 0-1 (should use interpolated):")
	t.Logf("  libopus PredCoef[0][0:5]: %v", libFrame1.PredCoef0Q12[:5])
	t.Log("For subframes 2-3 (should use full):")
	t.Logf("  libopus PredCoef[1][0:5]: %v", libFrame1.PredCoef1Q12[:5])
}

// TestNLSF2ADirectComparison compares NLSF to LPC conversion between gopus and libopus.
func TestNLSF2ADirectComparison(t *testing.T) {
	// Test NLSF values from packet 15 frame 0 (which is bit-exact)
	nlsfFrame0 := []int16{2676, 3684, 7247, 12558, 14555, 19131, 21376, 26092, 27957, 30426}

	// Get libopus LPC coefficients
	libLPC := SilkNLSF2A(nlsfFrame0, 10)
	t.Logf("Frame 0 NLSF: %v", nlsfFrame0)
	t.Logf("libopus LPC[0:5]: %v", libLPC[:5])

	// Now test interpolated NLSF for frame 1
	// prevNLSF = frame 0's NLSF
	// currNLSF = frame 1's NLSF
	prevNLSF := nlsfFrame0
	currNLSF := []int16{2701, 3363, 5756, 13031, 13464, 18831, 21312, 25831, 28019, 29426}
	interpCoef := int8(1)

	// Compute interpolated NLSF
	nlsf0 := make([]int16, 10)
	for i := 0; i < 10; i++ {
		diff := int32(currNLSF[i]) - int32(prevNLSF[i])
		nlsf0[i] = int16(int32(prevNLSF[i]) + (int32(interpCoef) * diff >> 2))
	}

	t.Logf("\nFrame 1 interpolation (coef=%d):", interpCoef)
	t.Logf("  prevNLSF: %v", prevNLSF[:5])
	t.Logf("  currNLSF: %v", currNLSF[:5])
	t.Logf("  interpNLSF (nlsf0): %v", nlsf0[:5])

	// Get libopus LPC for interpolated NLSF
	libLPC0 := SilkNLSF2A(nlsf0, 10)
	t.Logf("  libopus LPC for nlsf0[0:5]: %v", libLPC0[:5])

	// Get libopus LPC for currNLSF (PredCoef[1])
	libLPC1 := SilkNLSF2A(currNLSF, 10)
	t.Logf("  libopus LPC for currNLSF[0:5]: %v", libLPC1[:5])
}

// TestGopusNLSF2A tests gopus's silkNLSF2A function directly.
func TestGopusNLSF2A(t *testing.T) {
	// Test same NLSF values
	nlsfFrame0 := []int16{2676, 3684, 7247, 12558, 14555, 19131, 21376, 26092, 27957, 30426}

	// Get gopus LPC (we need to call the internal function)
	// For now, let's decode a packet and capture the values
	t.Log("Testing gopus NLSF2A vs libopus NLSF2A")
	t.Logf("Input NLSF: %v", nlsfFrame0[:5])

	libLPC := SilkNLSF2A(nlsfFrame0, 10)
	t.Logf("libopus LPC: %v", libLPC)

	// The expected values from libopus for frame 0 are:
	// PredCoef_Q12[0] (first 5): [3952 -3489 3995 -1295 1307]
	// Let's verify libopus NLSF2A produces these
	t.Log("\nExpected from actual packet decode:")
	t.Log("  libopus PredCoef_Q12[0]: [3952 -3489 3995 -1295 1307 ...]")
	t.Logf("  NLSF2A result:           %v", libLPC[:5])
}

// TestVerifyLibopusNLSFValues verifies the NLSF values from the decode state function.
func TestVerifyLibopusNLSFValues(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil || len(packets) < 16 {
		t.Skip("Could not load packets")
	}

	pkt := packets[15]
	toc := gopus.ParseTOC(pkt[0])

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	config := silk.GetBandwidthConfig(silkBW)

	fsKHz := config.SampleRate / 1000
	nbSubfr := 4
	framesPerPacket := 3
	lpcOrder := 10

	// Get NLSF state from libopus
	libStates, err := SilkDecodeNLSFState(pkt[1:], fsKHz, nbSubfr, framesPerPacket, framesPerPacket, lpcOrder)
	if err != nil || libStates == nil {
		t.Fatal("Could not decode NLSF state from libopus")
	}

	t.Log("Verifying NLSF values and LPC coefficients for each frame:")
	for i, st := range libStates {
		t.Logf("\n=== Frame %d ===", i)
		t.Logf("  NLSFInterpCoefQ2: %d (interp=%v)", st.NLSFInterpCoefQ2, st.NLSFInterpCoefQ2 < 4)
		t.Logf("  currNLSF_Q15: %v", st.CurrNLSFQ15)
		t.Logf("  PredCoef_Q12[1] from decode: %v", st.PredCoef1Q12)

		// Call NLSF2A directly with currNLSF
		directLPC := SilkNLSF2A(st.CurrNLSFQ15, lpcOrder)
		t.Logf("  NLSF2A(currNLSF) direct:     %v", directLPC[:lpcOrder])

		// Check if they match
		match := true
		for j := 0; j < lpcOrder; j++ {
			if st.PredCoef1Q12[j] != directLPC[j] {
				match = false
				break
			}
		}
		if !match {
			t.Log("  WARNING: PredCoef[1] doesn't match direct NLSF2A result!")
		} else {
			t.Log("  OK: PredCoef[1] matches direct NLSF2A result")
		}
	}
}

// TestNLSFDecodeComparison compares silkNLSFDecode between gopus and libopus.
func TestNLSFDecodeComparison(t *testing.T) {
	bitFile := "../../../internal/testvectors/testdata/opus_testvectors/testvector02.bit"
	packets, err := loadPacketsSimple(bitFile, 20)
	if err != nil || len(packets) < 16 {
		t.Skip("Could not load packets")
	}

	pkt := packets[15]
	toc := gopus.ParseTOC(pkt[0])

	silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
	if !ok {
		t.Skip("Invalid SILK bandwidth")
	}
	config := silk.GetBandwidthConfig(silkBW)

	fsKHz := config.SampleRate / 1000
	nbSubfr := 4
	framesPerPacket := 3
	frameLength := 160

	t.Log("Comparing NLSF decode between gopus and libopus:")

	// Get indices from libopus
	for frame := 0; frame < framesPerPacket; frame++ {
		libIndices, err := SilkDecodeIndicesPulses(pkt[1:], fsKHz, nbSubfr, framesPerPacket, frame, frameLength)
		if err != nil || libIndices == nil {
			t.Logf("Frame %d: Could not get libopus indices", frame)
			continue
		}

		t.Logf("\nFrame %d:", frame)
		t.Logf("  libopus NLSFIndices: %v", libIndices.NLSFIndices[:11])
		t.Logf("  libopus NLSFInterpCoef: %d", libIndices.NLSFInterpCoef)

		// Call libopus NLSF decode with these indices
		// Convert indices to int8 slice
		indices := make([]int8, 11)
		for i := 0; i < 11; i++ {
			indices[i] = libIndices.NLSFIndices[i]
		}

		// Use libopus to decode NLSF
		libNLSF := SilkNLSFDecode(indices, false) // false = use NB/MB codebook
		t.Logf("  libopus NLSF_Q15: %v", libNLSF[:10])
	}
}
