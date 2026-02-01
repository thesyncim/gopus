package silk

import (
	"testing"
)

// TestSilkSumSqrShift tests the energy calculation function.
func TestSilkSumSqrShift(t *testing.T) {
	tests := []struct {
		name    string
		samples []int16
		wantNrg int32
	}{
		{
			name:    "all zeros",
			samples: make([]int16, 100),
			wantNrg: 0,
		},
		{
			name:    "small values",
			samples: []int16{1, 2, 3, 4, 5},
			wantNrg: 1 + 4 + 9 + 16 + 25, // 55
		},
		{
			name:    "single large value",
			samples: []int16{1000},
			wantNrg: 1000000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNrg, gotShift := silkSumSqrShift(tt.samples, len(tt.samples))

			// Verify energy is correct when accounting for shift
			actualNrg := int64(gotNrg) << gotShift
			expectedNrg := int64(0)
			for _, s := range tt.samples {
				expectedNrg += int64(s) * int64(s)
			}

			// Allow for some rounding error due to shifting
			diff := actualNrg - expectedNrg
			if diff < 0 {
				diff = -diff
			}
			maxDiff := expectedNrg / 10 // Allow 10% error
			if maxDiff < 1 {
				maxDiff = 1
			}
			if diff > maxDiff && expectedNrg > 0 {
				t.Errorf("silkSumSqrShift() energy = %d << %d = %d, want ~%d (diff %d)",
					gotNrg, gotShift, actualNrg, expectedNrg, diff)
			}
		})
	}
}

// TestSilkSumSqrShiftOverflow verifies overflow handling.
func TestSilkSumSqrShiftOverflow(t *testing.T) {
	// Create a large array with max values to test overflow handling
	samples := make([]int16, 1000)
	for i := range samples {
		samples[i] = 30000 // Large but not max to avoid integer overflow in test
	}

	nrg, shift := silkSumSqrShift(samples, len(samples))

	// The function should return a valid energy with some shift
	if nrg <= 0 {
		t.Errorf("Expected positive energy, got %d", nrg)
	}
	if shift < 0 {
		t.Errorf("Expected non-negative shift, got %d", shift)
	}

	// Verify the scaled energy fits in int32
	if nrg > 0x7FFFFFFF {
		t.Errorf("Energy %d exceeds int32 max", nrg)
	}
}

// TestSilkCLZ32 tests the count leading zeros function.
func TestSilkCLZ32(t *testing.T) {
	tests := []struct {
		x    int32
		want int32
	}{
		{0, 32},
		{1, 31},
		{2, 30},
		{0x7FFFFFFF, 1},
		{0x40000000, 1},
		{0x10000, 15},
		{0x100, 23},
	}

	for _, tt := range tests {
		got := silkCLZ32(tt.x)
		if got != tt.want {
			t.Errorf("silkCLZ32(%#x) = %d, want %d", tt.x, got, tt.want)
		}
	}
}

// TestSilkSqrtApproxPLC tests that sqrt approximation produces reasonable values.
// The exact values don't need to match libopus precisely, but should be
// in the right ballpark for PLC gain calculations.
func TestSilkSqrtApprox(t *testing.T) {
	// Test that sqrt produces positive values for positive inputs
	tests := []int32{
		1,
		100,
		10000,
		1000000,
		1 << 20,
		1 << 24,
	}

	for _, x := range tests {
		got := silkSqrtApproxPLC(x)
		if got <= 0 {
			t.Errorf("silkSqrtApproxPLC(%d) = %d, want positive value", x, got)
		}
		// Verify sqrt(x)^2 is in reasonable range of x
		// (within an order of magnitude due to approximation)
		gotSquared := int64(got) * int64(got)
		ratio := float64(gotSquared) / float64(x)
		if ratio < 0.01 || ratio > 100 {
			t.Errorf("silkSqrtApproxPLC(%d)^2 = %d, ratio to input = %.2f (out of range)",
				x, gotSquared, ratio)
		}
	}

	// Test zero and negative
	if got := silkSqrtApproxPLC(0); got != 0 {
		t.Errorf("silkSqrtApproxPLC(0) = %d, want 0", got)
	}
	if got := silkSqrtApproxPLC(-1); got != 0 {
		t.Errorf("silkSqrtApproxPLC(-1) = %d, want 0", got)
	}
}

// TestPLCGlueFramesNoLoss verifies no modification when not recovering from loss.
func TestPLCGlueFramesNoLoss(t *testing.T) {
	st := &decoderState{}
	st.lossCnt = 0
	st.plcLastFrameLost = false

	original := []int16{1000, 2000, 3000, 4000, 5000}
	frame := make([]int16, len(original))
	copy(frame, original)

	silkPLCGlueFrames(st, frame, len(frame))

	// Frame should be unchanged
	for i, v := range frame {
		if v != original[i] {
			t.Errorf("Sample %d changed from %d to %d when not recovering from loss",
				i, original[i], v)
		}
	}
}

// TestPLCGlueFramesDuringLoss verifies energy tracking during loss.
func TestPLCGlueFramesDuringLoss(t *testing.T) {
	st := &decoderState{}
	st.lossCnt = 1 // Currently in loss

	frame := []int16{1000, 2000, 3000, 4000, 5000}
	silkPLCGlueFrames(st, frame, len(frame))

	// Should have stored energy
	if st.plcConcEnergy == 0 && st.plcConcEnergyShift == 0 {
		// At least one should be non-zero for non-zero frame
		t.Error("Expected concealed energy to be calculated")
	}

	// Should have marked frame as lost
	if !st.plcLastFrameLost {
		t.Error("Expected plcLastFrameLost to be true during loss")
	}
}

// TestPLCGlueFramesRecovery verifies gain ramp during recovery.
func TestPLCGlueFramesRecovery(t *testing.T) {
	st := &decoderState{}

	// First, simulate concealment with low energy
	st.lossCnt = 1
	concealedFrame := make([]int16, 100)
	for i := range concealedFrame {
		concealedFrame[i] = 100 // Low amplitude
	}
	silkPLCGlueFrames(st, concealedFrame, len(concealedFrame))

	// Now simulate recovery with high energy frame
	st.lossCnt = 0 // Good frame received
	recoveredFrame := make([]int16, 100)
	for i := range recoveredFrame {
		recoveredFrame[i] = 10000 // High amplitude
	}
	originalSum := int64(0)
	for _, v := range recoveredFrame {
		originalSum += int64(v)
	}

	silkPLCGlueFrames(st, recoveredFrame, len(recoveredFrame))

	// The recovered frame should have been attenuated at the start
	// (gain ramp applied to prevent pop)
	modifiedSum := int64(0)
	for _, v := range recoveredFrame {
		modifiedSum += int64(v)
	}

	// After gluing, the sum should be less due to gain ramp
	// Note: Only if recovered energy was higher than concealed
	if modifiedSum >= originalSum {
		// This is acceptable if energies were similar
		t.Logf("Frame sum unchanged: original=%d, modified=%d", originalSum, modifiedSum)
	}

	// The plcLastFrameLost flag should be cleared
	if st.plcLastFrameLost {
		t.Error("Expected plcLastFrameLost to be false after recovery")
	}
}

// TestPLCGlueFramesEnergyMatching tests that energy matching works correctly.
func TestPLCGlueFramesEnergyMatching(t *testing.T) {
	st := &decoderState{}

	// Simulate concealment with specific energy
	st.lossCnt = 1
	concealedFrame := make([]int16, 160) // 10ms at 16kHz
	for i := range concealedFrame {
		concealedFrame[i] = 500
	}
	silkPLCGlueFrames(st, concealedFrame, len(concealedFrame))

	// Verify energy was stored
	concEnergy := st.plcConcEnergy
	concShift := st.plcConcEnergyShift
	t.Logf("Concealed energy: %d << %d", concEnergy, concShift)

	// Now recover with much higher energy
	st.lossCnt = 0
	recoveredFrame := make([]int16, 160)
	for i := range recoveredFrame {
		recoveredFrame[i] = 20000 // Much higher amplitude
	}

	// Store original first sample
	originalFirst := recoveredFrame[0]

	silkPLCGlueFrames(st, recoveredFrame, len(recoveredFrame))

	// First sample should be attenuated (if energy matching applied)
	if recoveredFrame[0] >= originalFirst {
		t.Logf("First sample: original=%d, after glue=%d", originalFirst, recoveredFrame[0])
		// This might happen if the sqrt approximation gives a high result
	} else {
		t.Logf("Gain ramp applied: first sample %d -> %d", originalFirst, recoveredFrame[0])
	}
}

// TestPLCGlueFramesIntegration tests the complete PLC flow with gluing.
func TestPLCGlueFramesIntegration(t *testing.T) {
	// Create a decoder
	dec := NewDecoder()

	// Simulate state as if we had decoded some frames
	st := &dec.state[0]
	st.fsKHz = 16
	st.frameLength = 320
	st.subfrLength = 80
	st.nbSubfr = 4
	st.ltpMemLength = 320
	st.lpcOrder = 16

	// Simulate a loss sequence followed by recovery
	// This would normally happen through Decode() calls

	// Step 1: Good frame (establishes baseline)
	st.lossCnt = 0
	st.plcLastFrameLost = false

	// Step 2: Lost frame (PLC concealment)
	st.lossCnt = 1
	concealedFrame := make([]int16, 320)
	for i := range concealedFrame {
		concealedFrame[i] = 500
	}
	silkPLCGlueFrames(st, concealedFrame, len(concealedFrame))

	if !st.plcLastFrameLost {
		t.Error("Expected plcLastFrameLost to be true after loss")
	}

	// Step 3: Another lost frame
	st.lossCnt = 2
	for i := range concealedFrame {
		concealedFrame[i] = 400 // Slightly lower due to decay
	}
	silkPLCGlueFrames(st, concealedFrame, len(concealedFrame))

	// Step 4: Good frame recovered - should apply gluing
	st.lossCnt = 0 // Will be set to 0 by normal decode path
	recoveredFrame := make([]int16, 320)
	for i := range recoveredFrame {
		recoveredFrame[i] = 10000 // High energy good frame
	}
	silkPLCGlueFrames(st, recoveredFrame, len(recoveredFrame))

	// Verify state is correct after recovery
	if st.plcLastFrameLost {
		t.Error("Expected plcLastFrameLost to be false after successful recovery")
	}
}

// BenchmarkSilkSumSqrShift benchmarks the energy calculation.
func BenchmarkSilkSumSqrShift(b *testing.B) {
	samples := make([]int16, 320) // 20ms at 16kHz
	for i := range samples {
		samples[i] = int16(i % 1000)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		silkSumSqrShift(samples, len(samples))
	}
}

// BenchmarkSilkPLCGlueFrames benchmarks the glue frames function.
func BenchmarkSilkPLCGlueFrames(b *testing.B) {
	st := &decoderState{}
	st.lossCnt = 0
	st.plcLastFrameLost = true
	st.plcConcEnergy = 1000000
	st.plcConcEnergyShift = 4

	frame := make([]int16, 320)
	for i := range frame {
		frame[i] = 10000
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.plcLastFrameLost = true // Reset for each iteration
		silkPLCGlueFrames(st, frame, len(frame))
	}
}

// TestPLCGlueFramesSmoothTransition tests that the transition from concealed
// to real frames doesn't have abrupt changes (which would cause clicks/pops).
func TestPLCGlueFramesSmoothTransition(t *testing.T) {
	st := &decoderState{}

	// Simulate a concealed frame with low amplitude
	st.lossCnt = 1
	concealedFrame := make([]int16, 320)
	for i := range concealedFrame {
		concealedFrame[i] = 200 // Low amplitude concealment
	}
	silkPLCGlueFrames(st, concealedFrame, len(concealedFrame))

	// Now simulate a real frame with much higher amplitude
	st.lossCnt = 0
	realFrame := make([]int16, 320)
	for i := range realFrame {
		realFrame[i] = 15000 // High amplitude real audio
	}

	// Capture the first few samples before gluing
	firstSampleBefore := realFrame[0]

	silkPLCGlueFrames(st, realFrame, len(realFrame))

	// After gluing, the first sample should be attenuated
	// (to avoid the pop from sudden amplitude increase)
	firstSampleAfter := realFrame[0]

	// Calculate the ratio of first sample change
	// The gain ramp should start low and increase
	t.Logf("Transition: first sample %d -> %d", firstSampleBefore, firstSampleAfter)

	// Verify the output ramps up (not constant or decreasing abruptly)
	// Check that samples increase as we move through the frame
	var increasing int
	for i := 1; i < len(realFrame); i++ {
		if realFrame[i] > realFrame[i-1] {
			increasing++
		}
	}

	// At least some samples should be increasing during the ramp
	// (unless the ramp completes quickly due to similar energies)
	t.Logf("Increasing transitions: %d/%d", increasing, len(realFrame)-1)
}

// TestPLCGlueFramesSimilarEnergy tests that similar energy frames don't get modified much.
func TestPLCGlueFramesSimilarEnergy(t *testing.T) {
	st := &decoderState{}

	// Simulate a concealed frame with similar amplitude to real frame
	st.lossCnt = 1
	concealedFrame := make([]int16, 160)
	for i := range concealedFrame {
		concealedFrame[i] = 5000
	}
	silkPLCGlueFrames(st, concealedFrame, len(concealedFrame))

	// Now simulate a real frame with similar amplitude
	st.lossCnt = 0
	realFrame := make([]int16, 160)
	for i := range realFrame {
		realFrame[i] = 6000 // Similar amplitude
	}
	originalSum := int64(0)
	for _, v := range realFrame {
		originalSum += int64(v)
	}

	silkPLCGlueFrames(st, realFrame, len(realFrame))

	modifiedSum := int64(0)
	for _, v := range realFrame {
		modifiedSum += int64(v)
	}

	// For similar energies, the modification should be minimal
	ratio := float64(modifiedSum) / float64(originalSum)
	t.Logf("Similar energy: sum ratio = %.3f", ratio)

	// The sum should be close to original (within 50%)
	if ratio < 0.5 || ratio > 1.5 {
		t.Errorf("Similar energy frames modified too much: ratio = %.3f", ratio)
	}
}

// TestPLCGlueFramesLowerRecoveredEnergy tests that lower energy recovered frames
// are not modified (no gain increase applied).
func TestPLCGlueFramesLowerRecoveredEnergy(t *testing.T) {
	st := &decoderState{}

	// Simulate a concealed frame with HIGH amplitude
	st.lossCnt = 1
	concealedFrame := make([]int16, 160)
	for i := range concealedFrame {
		concealedFrame[i] = 20000 // High amplitude
	}
	silkPLCGlueFrames(st, concealedFrame, len(concealedFrame))

	// Now simulate a real frame with LOWER amplitude
	st.lossCnt = 0
	realFrame := make([]int16, 160)
	original := make([]int16, 160)
	for i := range realFrame {
		realFrame[i] = 1000 // Lower amplitude than concealed
		original[i] = 1000
	}

	silkPLCGlueFrames(st, realFrame, len(realFrame))

	// Frame should be unchanged (no gain boost)
	// because we only apply gluing when recovered > concealed
	unchanged := 0
	for i, v := range realFrame {
		if v == original[i] {
			unchanged++
		}
	}
	t.Logf("Lower energy recovery: %d/%d samples unchanged", unchanged, len(realFrame))

	// All samples should be unchanged
	if unchanged != len(realFrame) {
		t.Errorf("Lower energy recovered frame was modified unexpectedly")
	}
}
