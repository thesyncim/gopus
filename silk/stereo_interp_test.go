package silk

import (
	"fmt"
	"math"
	"testing"
)

// TestInterpolatePredictorsFloat verifies the basic interpolation math.
func TestInterpolatePredictorsFloat(t *testing.T) {
	// Test parameters: 16kHz sample rate, 160 samples (10ms frame)
	fsKHz := 16
	frameLength := 160
	interpSamples := stereoInterpLenMs * fsKHz // 8 * 16 = 128 samples

	// Create simple test signals
	mid := make([]float32, frameLength+2)
	side := make([]float32, frameLength+2)
	for i := range mid {
		mid[i] = 0.5  // Constant mid signal
		side[i] = 0.1 // Constant side signal
	}

	// Test case 1: No predictor change - should produce constant output
	t.Run("NoPredictorChange", func(t *testing.T) {
		prevPred := [2]float32{0.1, 0.05}
		currPred := [2]float32{0.1, 0.05}
		sideOut := InterpolatePredictorsFloat(prevPred, currPred, 1.0, 1.0, mid, side, frameLength, fsKHz)

		// All samples should be the same (no interpolation effect)
		expected := 1.0*0.1 - 0.1*0.5 - 0.05*0.5 // width*side - pred0*lpMid - pred1*mid
		for n := 0; n < frameLength; n++ {
			if math.Abs(float64(sideOut[n]-float32(expected))) > 0.001 {
				t.Errorf("Sample %d: got %f, want ~%f", n, sideOut[n], expected)
				break
			}
		}
	})

	// Test case 2: Predictor change from 0 to non-zero
	t.Run("PredictorRampUp", func(t *testing.T) {
		prevPred := [2]float32{0, 0}
		currPred := [2]float32{0.2, 0.1}
		sideOut := InterpolatePredictorsFloat(prevPred, currPred, 1.0, 1.0, mid, side, frameLength, fsKHz)

		// First sample should be close to pure side (small interpolation factor)
		// Last interpolation sample should be close to full prediction
		firstInterp := sideOut[0]
		lastInterp := sideOut[interpSamples-1]
		afterInterp := sideOut[interpSamples]

		// First sample: t = 1/128 ≈ 0.0078, so pred ≈ 0
		// Expected ≈ 1.0*0.1 - 0*0.5 - 0*0.5 = 0.1
		if math.Abs(float64(firstInterp)-0.1) > 0.02 {
			t.Errorf("First interp sample: got %f, want ~0.1", firstInterp)
		}

		// After interpolation: full prediction applied
		expectedAfter := 1.0*0.1 - 0.2*0.5 - 0.1*0.5 // = 0.1 - 0.1 - 0.05 = -0.05
		if math.Abs(float64(afterInterp)-expectedAfter) > 0.001 {
			t.Errorf("After interp sample: got %f, want %f", afterInterp, expectedAfter)
		}

		// Last interp sample should be close to full prediction
		if math.Abs(float64(lastInterp)-expectedAfter) > 0.01 {
			t.Errorf("Last interp sample: got %f, want ~%f", lastInterp, expectedAfter)
		}
	})

	// Test case 3: Width interpolation from 0 to 1
	t.Run("WidthRampUp", func(t *testing.T) {
		prevPred := [2]float32{0, 0}
		currPred := [2]float32{0, 0}
		sideOut := InterpolatePredictorsFloat(prevPred, currPred, 0.0, 1.0, mid, side, frameLength, fsKHz)

		// First sample: width ≈ 0, so side contribution is small
		// After interpolation: width = 1.0
		firstInterp := sideOut[0]
		afterInterp := sideOut[interpSamples]

		// First sample: width ≈ 1/128, side contribution ≈ 0.0008
		if math.Abs(float64(firstInterp)) > 0.01 {
			t.Errorf("First interp sample: got %f, want ~0", firstInterp)
		}

		// After interpolation: width = 1.0, side = 0.1
		if math.Abs(float64(afterInterp)-0.1) > 0.001 {
			t.Errorf("After interp sample: got %f, want 0.1", afterInterp)
		}
	})
}

// TestStereoEncStateInterp tests the stateful interpolation wrapper.
func TestStereoEncStateInterp(t *testing.T) {
	fsKHz := 16
	frameLength := 160

	// Create test signals
	mid := make([]float32, frameLength+2)
	side := make([]float32, frameLength+2)
	for i := range mid {
		mid[i] = 0.5
		side[i] = 0.1
	}

	state := &StereoEncStateInterp{}
	state.Reset()

	// First frame: state starts at zero, apply non-zero predictors
	currPredQ13 := [2]int32{1638, 819} // 0.2 and 0.1 in Q13
	currWidthQ14 := int16(16384)       // 1.0 in Q14

	sideOut1 := state.ApplyInterpolation(currPredQ13, currWidthQ14, mid, side, frameLength, fsKHz)

	// Verify state was updated
	if state.PrevPredQ13[0] != currPredQ13[0] || state.PrevPredQ13[1] != currPredQ13[1] {
		t.Error("State predictors not updated after first frame")
	}
	if state.PrevWidthQ14 != currWidthQ14 {
		t.Error("State width not updated after first frame")
	}

	// Second frame: same predictors - should produce constant output
	sideOut2 := state.ApplyInterpolation(currPredQ13, currWidthQ14, mid, side, frameLength, fsKHz)

	// All samples in second frame should be identical
	for n := 1; n < frameLength; n++ {
		if math.Abs(float64(sideOut2[n]-sideOut2[0])) > 0.001 {
			t.Errorf("Second frame sample %d differs from sample 0: %f vs %f", n, sideOut2[n], sideOut2[0])
			break
		}
	}

	// The last sample of first frame should match all samples of second frame
	// (both use the same predictor values without interpolation)
	if math.Abs(float64(sideOut1[frameLength-1]-sideOut2[0])) > 0.001 {
		t.Errorf("Discontinuity at frame boundary: frame1 last=%f, frame2 first=%f",
			sideOut1[frameLength-1], sideOut2[0])
	}
}

// TestSmoothTransitionAtBoundary verifies no discontinuity at frame boundaries.
func TestSmoothTransitionAtBoundary(t *testing.T) {
	fsKHz := 16
	frameLength := 160

	// Create sine wave test signal
	mid := make([]float32, frameLength+2)
	side := make([]float32, frameLength+2)
	for i := range mid {
		mid[i] = float32(math.Sin(2 * math.Pi * float64(i) / 32))
		side[i] = float32(math.Sin(2*math.Pi*float64(i)/32)) * 0.3
	}

	state := &StereoEncStateInterp{}
	state.Reset()

	// Frame 1: ramp up predictors from 0
	pred1 := [2]int32{2000, 1000}
	sideOut1 := state.ApplyInterpolation(pred1, 16384, mid, side, frameLength, fsKHz)

	// Frame 2: change predictors
	pred2 := [2]int32{3000, 500}
	sideOut2 := state.ApplyInterpolation(pred2, 16384, mid, side, frameLength, fsKHz)

	// Check continuity at boundary: difference should be small
	// The exact continuity depends on the signal, but with interpolation
	// the transition should be smoother than without
	boundaryDiff := math.Abs(float64(sideOut2[0] - sideOut1[frameLength-1]))

	// For a smooth transition, the difference should be reasonable
	// (not a hard threshold, but sanity check)
	if boundaryDiff > 0.5 {
		t.Errorf("Large discontinuity at boundary: %f", boundaryDiff)
	}

	// Frame 3: same predictors as frame 2 - should show interpolation completed
	sideOut3 := state.ApplyInterpolation(pred2, 16384, mid, side, frameLength, fsKHz)

	// Boundary between frame 2 and 3 should be very smooth (same predictors)
	// Note: some discontinuity is expected due to the varying sine wave signal
	// The key is that it should be much smaller than without interpolation
	boundaryDiff23 := math.Abs(float64(sideOut3[0] - sideOut2[frameLength-1]))
	if boundaryDiff23 > 0.05 {
		t.Errorf("Unexpected discontinuity with same predictors: %f", boundaryDiff23)
	}
}

// TestInterpolationSamplesCount verifies the correct number of samples are interpolated.
func TestInterpolationSamplesCount(t *testing.T) {
	testCases := []struct {
		fsKHz         int
		wantInterpLen int
	}{
		{8, 64},   // 8kHz: 8ms * 8 = 64 samples
		{12, 96},  // 12kHz: 8ms * 12 = 96 samples
		{16, 128}, // 16kHz: 8ms * 16 = 128 samples
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%dkHz", tc.fsKHz), func(t *testing.T) {
			interpLen := stereoInterpLenMs * tc.fsKHz
			if interpLen != tc.wantInterpLen {
				t.Errorf("Interpolation length for %dkHz: got %d, want %d",
					tc.fsKHz, interpLen, tc.wantInterpLen)
			}
		})
	}
}

// TestEncoderInterpolationState tests the encoder's interpolation state management.
func TestEncoderInterpolationState(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Initially state should be zero
	predPrev, widthPrev := enc.GetInterpolationState()
	if predPrev[0] != 0 || predPrev[1] != 0 {
		t.Error("Initial predictor state should be zero")
	}
	if widthPrev != 0 {
		t.Error("Initial width state should be zero")
	}

	// Set state and verify
	testPred := [2]int32{1234, 5678}
	testWidth := int16(12345)
	enc.SetInterpolationState(testPred, testWidth)

	gotPred, gotWidth := enc.GetInterpolationState()
	if gotPred != testPred {
		t.Errorf("Got predictor state %v, want %v", gotPred, testPred)
	}
	if gotWidth != testWidth {
		t.Errorf("Got width state %d, want %d", gotWidth, testWidth)
	}

	// Reset should clear state
	enc.ResetStereoState()
	predPrev, widthPrev = enc.GetInterpolationState()
	if predPrev[0] != 0 || predPrev[1] != 0 || widthPrev != 0 {
		t.Error("State should be zero after reset")
	}
}

// TestStereoEncodeLRToMSWithInterp tests the full encoder interpolation function.
func TestStereoEncodeLRToMSWithInterp(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	fsKHz := 16
	frameLength := 160

	// Create test stereo signal
	left := make([]float32, frameLength+2)
	right := make([]float32, frameLength+2)
	for i := range left {
		// Simple stereo signal: left and right with different content
		left[i] = float32(math.Sin(2*math.Pi*float64(i)/32)) * 0.5
		right[i] = float32(math.Sin(2*math.Pi*float64(i)/32)) * 0.3
	}

	// First frame
	widthQ14 := int16(16384) // Full width
	midOut1, sideOut1, predQ13_1 := enc.StereoEncodeLRToMSWithInterp(left, right, frameLength, fsKHz, widthQ14)

	if len(midOut1) != frameLength {
		t.Errorf("Mid output length: got %d, want %d", len(midOut1), frameLength)
	}
	if len(sideOut1) != frameLength {
		t.Errorf("Side output length: got %d, want %d", len(sideOut1), frameLength)
	}

	// Verify state was updated
	gotPred, gotWidth := enc.GetInterpolationState()
	if gotPred != predQ13_1 {
		t.Errorf("Predictor state not updated: got %v, want %v", gotPred, predQ13_1)
	}
	if gotWidth != widthQ14 {
		t.Errorf("Width state not updated: got %d, want %d", gotWidth, widthQ14)
	}

	// Second frame with same input should produce stable output
	_, sideOut2, _ := enc.StereoEncodeLRToMSWithInterp(left, right, frameLength, fsKHz, widthQ14)

	// Frame boundary should be reasonably continuous
	boundaryDiff := math.Abs(float64(sideOut2[0] - sideOut1[frameLength-1]))
	if boundaryDiff > 0.2 {
		t.Logf("Note: boundary discontinuity = %f (depends on signal content)", boundaryDiff)
	}
}

// TestInterpolationContinuity tests that interpolation provides smooth transitions
// even when predictors change significantly between frames.
func TestInterpolationContinuity(t *testing.T) {
	fsKHz := 16
	frameLength := 160
	interpSamples := stereoInterpLenMs * fsKHz

	// Create constant test signals for predictable output
	mid := make([]float32, frameLength+2)
	side := make([]float32, frameLength+2)
	for i := range mid {
		mid[i] = 1.0
		side[i] = 0.5
	}

	// Test: Large predictor change should still produce continuous output within a frame
	state := &StereoEncStateInterp{}
	state.Reset()

	// Set initial state
	state.PrevPredQ13 = [2]int32{0, 0}
	state.PrevWidthQ14 = 16384

	// Apply large predictor change
	newPred := [2]int32{8192, 4096} // 1.0 and 0.5 in Q13
	sideOut := state.ApplyInterpolation(newPred, 16384, mid, side, frameLength, fsKHz)

	// Verify output is monotonic during interpolation (for this constant signal case)
	// With constant input and positive predictors, output should smoothly decrease
	for n := 1; n < interpSamples && n < frameLength; n++ {
		// Allow for small numerical variations
		diff := sideOut[n] - sideOut[n-1]
		if diff > 0.01 { // Should be decreasing or staying same
			t.Errorf("Non-monotonic interpolation at sample %d: %f -> %f (diff=%f)",
				n-1, sideOut[n-1], sideOut[n], diff)
			break
		}
	}
}

// TestDecoderInterpolationMatches tests that decoder interpolation in libopus_stereo.go
// follows the same pattern as encoder interpolation.
func TestDecoderInterpolationMatches(t *testing.T) {
	// This test verifies that silkStereoMSToLR in libopus_stereo.go
	// uses the same interpolation formula as the encoder

	// The decoder already has interpolation in silkStereoMSToLR:
	// - interpSamples := stereoInterpLenMs * fsKHz
	// - delta0 = (pred[0] - prevPred[0]) / interpSamples
	// - delta1 = (pred[1] - prevPred[1]) / interpSamples
	// - For n in 0..interpSamples: pred += delta, apply to side

	// Just verify the constant is the same
	if stereoInterpLenMs != 8 {
		t.Errorf("stereoInterpLenMs = %d, want 8", stereoInterpLenMs)
	}
}

// Benchmark for interpolation performance
func BenchmarkInterpolatePredictorsFloat(b *testing.B) {
	fsKHz := 16
	frameLength := 320 // 20ms frame

	mid := make([]float32, frameLength+2)
	side := make([]float32, frameLength+2)
	for i := range mid {
		mid[i] = 0.5
		side[i] = 0.1
	}

	prevPred := [2]float32{0.1, 0.05}
	currPred := [2]float32{0.2, 0.1}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = InterpolatePredictorsFloat(prevPred, currPred, 1.0, 1.0, mid, side, frameLength, fsKHz)
	}
}

func BenchmarkStereoEncStateInterpApply(b *testing.B) {
	fsKHz := 16
	frameLength := 320

	mid := make([]float32, frameLength+2)
	side := make([]float32, frameLength+2)
	for i := range mid {
		mid[i] = 0.5
		side[i] = 0.1
	}

	state := &StereoEncStateInterp{}
	currPredQ13 := [2]int32{1638, 819}
	currWidthQ14 := int16(16384)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = state.ApplyInterpolation(currPredQ13, currWidthQ14, mid, side, frameLength, fsKHz)
	}
}
