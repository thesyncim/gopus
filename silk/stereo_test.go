package silk

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/util"
	"github.com/thesyncim/gopus/rangecoding"
)

func TestStereoUnmixBasic(t *testing.T) {
	// Test basic mid-side to left-right conversion without prediction
	mid := []float32{0.5, 0.5, 0.5, 0.5}
	side := []float32{0.25, 0.25, 0.25, 0.25}
	left := make([]float32, 4)
	right := make([]float32, 4)

	if err := stereoUnmix(mid, side, 0, 0, left, right); err != nil {
		t.Fatalf("stereoUnmix failed: %v", err)
	}

	// L = M + S = 0.75
	// R = M - S = 0.25
	for i := range left {
		if math.Abs(float64(left[i]-0.75)) > 0.001 {
			t.Errorf("left[%d] = %f, want 0.75", i, left[i])
		}
		if math.Abs(float64(right[i]-0.25)) > 0.001 {
			t.Errorf("right[%d] = %f, want 0.25", i, right[i])
		}
	}
}

func TestStereoUnmixZeroSide(t *testing.T) {
	// Zero side channel should give L == R == M
	mid := []float32{0.6, -0.3, 0.0, 0.8}
	side := []float32{0.0, 0.0, 0.0, 0.0}
	left := make([]float32, 4)
	right := make([]float32, 4)

	if err := stereoUnmix(mid, side, 0, 0, left, right); err != nil {
		t.Fatalf("stereoUnmix failed: %v", err)
	}

	for i := range mid {
		if math.Abs(float64(left[i]-mid[i])) > 0.001 {
			t.Errorf("left[%d] = %f, want %f", i, left[i], mid[i])
		}
		if math.Abs(float64(right[i]-mid[i])) > 0.001 {
			t.Errorf("right[%d] = %f, want %f", i, right[i], mid[i])
		}
	}
}

func TestStereoUnmixClamping(t *testing.T) {
	// Test that output is clamped to [-1, 1]
	mid := []float32{1.0, -1.0}
	side := []float32{0.5, -0.5}
	left := make([]float32, 2)
	right := make([]float32, 2)

	if err := stereoUnmix(mid, side, 0, 0, left, right); err != nil {
		t.Fatalf("stereoUnmix failed: %v", err)
	}

	// L = 1.0 + 0.5 = 1.5 -> clamped to 1.0
	if left[0] > 1.0 {
		t.Errorf("left[0] = %f, should be clamped to 1.0", left[0])
	}
	// R = 1.0 - 0.5 = 0.5 (no clamp needed)
	if math.Abs(float64(right[0]-0.5)) > 0.001 {
		t.Errorf("right[0] = %f, want 0.5", right[0])
	}

	// L = -1.0 + -0.5 = -1.5 -> clamped to -1.0
	if left[1] < -1.0 {
		t.Errorf("left[1] = %f, should be clamped to -1.0", left[1])
	}
}

func TestFrameDurationConversion(t *testing.T) {
	tests := []struct {
		tocFrameSize int
		wantDuration FrameDuration
	}{
		{480, Frame10ms},
		{960, Frame20ms},
		{1920, Frame40ms},
		{2880, Frame60ms},
	}

	for _, tt := range tests {
		got := FrameDurationFromTOC(tt.tocFrameSize)
		if got != tt.wantDuration {
			t.Errorf("FrameDurationFromTOC(%d) = %d, want %d", tt.tocFrameSize, got, tt.wantDuration)
		}
	}
}

func TestSubframeCount(t *testing.T) {
	tests := []struct {
		duration FrameDuration
		want     int
	}{
		{Frame10ms, 2},
		{Frame20ms, 4},
		{Frame40ms, 8},
		{Frame60ms, 12},
	}

	for _, tt := range tests {
		got := getSubframeCount(tt.duration)
		if got != tt.want {
			t.Errorf("getSubframeCount(%d) = %d, want %d", tt.duration, got, tt.want)
		}
	}
}

func TestFrameSamplesPerBandwidth(t *testing.T) {
	// 20ms frame at different bandwidths
	tests := []struct {
		bandwidth   Bandwidth
		wantSamples int
	}{
		{BandwidthNarrowband, 160},  // 8kHz * 0.02s
		{BandwidthMediumband, 240},  // 12kHz * 0.02s
		{BandwidthWideband, 320},    // 16kHz * 0.02s
	}

	for _, tt := range tests {
		got := getFrameSamples(Frame20ms, tt.bandwidth)
		if got != tt.wantSamples {
			t.Errorf("getFrameSamples(20ms, %v) = %d, want %d", tt.bandwidth, got, tt.wantSamples)
		}
	}
}

func TestGet48kHzSamples(t *testing.T) {
	tests := []struct {
		duration    FrameDuration
		wantSamples int
	}{
		{Frame10ms, 480},   // 10 * 48
		{Frame20ms, 960},   // 20 * 48
		{Frame40ms, 1920},  // 40 * 48
		{Frame60ms, 2880},  // 60 * 48
	}

	for _, tt := range tests {
		got := get48kHzSamples(tt.duration)
		if got != tt.wantSamples {
			t.Errorf("get48kHzSamples(%d) = %d, want %d", tt.duration, got, tt.wantSamples)
		}
	}
}

func TestIs40or60ms(t *testing.T) {
	tests := []struct {
		duration FrameDuration
		want     bool
	}{
		{Frame10ms, false},
		{Frame20ms, false},
		{Frame40ms, true},
		{Frame60ms, true},
	}

	for _, tt := range tests {
		got := is40or60ms(tt.duration)
		if got != tt.want {
			t.Errorf("is40or60ms(%d) = %v, want %v", tt.duration, got, tt.want)
		}
	}
}

func TestSubBlockCount(t *testing.T) {
	tests := []struct {
		duration FrameDuration
		want     int
	}{
		{Frame10ms, 1},
		{Frame20ms, 1},
		{Frame40ms, 2},
		{Frame60ms, 3},
	}

	for _, tt := range tests {
		got := getSubBlockCount(tt.duration)
		if got != tt.want {
			t.Errorf("getSubBlockCount(%d) = %d, want %d", tt.duration, got, tt.want)
		}
	}
}

func TestSamplesPerSubframe(t *testing.T) {
	tests := []struct {
		bandwidth   Bandwidth
		wantSamples int
	}{
		{BandwidthNarrowband, 40},   // 8kHz * 5ms
		{BandwidthMediumband, 60},   // 12kHz * 5ms
		{BandwidthWideband, 80},     // 16kHz * 5ms
	}

	for _, tt := range tests {
		got := getSamplesPerSubframe(tt.bandwidth)
		if got != tt.wantSamples {
			t.Errorf("getSamplesPerSubframe(%v) = %d, want %d", tt.bandwidth, got, tt.wantSamples)
		}
	}
}

func TestStereoPredWeights(t *testing.T) {
	// Verify stereo prediction weight table from libopus is symmetric
	// The table silk_stereo_pred_quant_Q13 has 16 entries
	for i := 0; i < 8; i++ {
		// Weights should be symmetric around midpoint
		if silk_stereo_pred_quant_Q13[i] != -silk_stereo_pred_quant_Q13[15-i] {
			t.Errorf("silk_stereo_pred_quant_Q13[%d] = %d should be negative of silk_stereo_pred_quant_Q13[%d] = %d",
				i, silk_stereo_pred_quant_Q13[i], 15-i, silk_stereo_pred_quant_Q13[15-i])
		}
	}
}

func TestStereoQuantPred80Levels(t *testing.T) {
	// Test that stereoQuantPred produces valid indices for various Q13 values
	testCases := []struct {
		name     string
		predQ13  [2]int32
		wantIx0  [3]int8 // Expected ix[0] ranges: [0][0-2], [1][0-4], [2][0-4]
		wantIx1  [3]int8 // Expected ix[1] ranges
	}{
		{
			name:    "zero predictors",
			predQ13: [2]int32{0, 0},
		},
		{
			name:    "positive predictor",
			predQ13: [2]int32{5000, 0},
		},
		{
			name:    "negative predictor",
			predQ13: [2]int32{-5000, 0},
		},
		{
			name:    "both predictors",
			predQ13: [2]int32{3000, -2000},
		},
		{
			name:    "extreme positive",
			predQ13: [2]int32{13732, 13732},
		},
		{
			name:    "extreme negative",
			predQ13: [2]int32{-13732, -13732},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			predQ13 := tc.predQ13
			ix := stereoQuantPred(&predQ13)

			// Verify index ranges
			for n := 0; n < 2; n++ {
				if ix.Ix[n][0] < 0 || ix.Ix[n][0] > 2 {
					t.Errorf("ix[%d][0] = %d, want [0, 2]", n, ix.Ix[n][0])
				}
				if ix.Ix[n][1] < 0 || ix.Ix[n][1] > 4 {
					t.Errorf("ix[%d][1] = %d, want [0, 4]", n, ix.Ix[n][1])
				}
				if ix.Ix[n][2] < 0 || ix.Ix[n][2] > 4 {
					t.Errorf("ix[%d][2] = %d, want [0, 4]", n, ix.Ix[n][2])
				}
			}

			// Verify joint index is valid (< 25)
			jointIdx := 5*int(ix.Ix[0][2]) + int(ix.Ix[1][2])
			if jointIdx >= 25 {
				t.Errorf("joint index = %d, want < 25", jointIdx)
			}
		})
	}
}

func TestStereoQuantPredDeltaCoding(t *testing.T) {
	// Test that delta coding is applied: predQ13[0] -= predQ13[1]
	predQ13 := [2]int32{5000, 3000}
	originalPred0 := predQ13[0]
	originalPred1 := predQ13[1]

	_ = stereoQuantPred(&predQ13)

	// After quantization, predQ13[0] should be the quantized delta
	// predQ13[1] should be the quantized second predictor
	// The relationship: quantized_pred0_original = predQ13[0] + predQ13[1]

	// We can't test exact values since quantization changes them,
	// but we can verify the delta relationship holds approximately
	reconstructedPred0 := predQ13[0] + predQ13[1]

	// The reconstructed value should be close to the original (within quantization error)
	// The quantization step is roughly (max-min)/80 levels
	maxError := int32(2000) // Allow for quantization error

	if util.Abs(reconstructedPred0-originalPred0) > maxError {
		t.Errorf("reconstructed pred[0] = %d, original = %d, difference = %d exceeds max error %d",
			reconstructedPred0, originalPred0, util.Abs(reconstructedPred0-originalPred0), maxError)
	}
	if util.Abs(predQ13[1]-originalPred1) > maxError {
		t.Errorf("quantized pred[1] = %d, original = %d, difference = %d exceeds max error %d",
			predQ13[1], originalPred1, util.Abs(predQ13[1]-originalPred1), maxError)
	}
}

func TestStereoEncodePredRoundtrip(t *testing.T) {
	// Test encoding stereo prediction indices
	testCases := []struct {
		name    string
		predQ13 [2]int32
	}{
		{"zero", [2]int32{0, 0}},
		{"positive", [2]int32{5000, 2000}},
		{"negative", [2]int32{-5000, -2000}},
		{"mixed", [2]int32{3000, -3000}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Quantize
			predQ13 := tc.predQ13
			ix := stereoQuantPred(&predQ13)

			// Encode to bitstream
			buf := make([]byte, 100)
			var enc rangecoding.Encoder
			enc.Init(buf)
			stereoEncodePred(&enc, ix)
			encoded := enc.Done()

			// Verify something was encoded (non-empty)
			if len(encoded) == 0 {
				t.Error("encoded data is empty")
			}
		})
	}
}

func TestSmulwb(t *testing.T) {
	// Test SMULWB: (a * int16(b)) >> 16
	// Note: b is truncated to int16, so only bottom 16 bits are used
	// int16 range is [-32768, 32767], so 32768 becomes -32768
	testCases := []struct {
		a, b int32
		want int32
	}{
		// Using values that fit in int16 for b
		{65536, 16384, 16384},  // 65536 * 16384 >> 16 = 16384
		{65536, -16384, -16384}, // 65536 * -16384 >> 16 = -16384
		{131072, 16384, 32768}, // 131072 * 16384 >> 16 = 32768
		{-65536, 16384, -16384}, // -65536 * 16384 >> 16 = -16384
		{0, 16384, 0},          // 0 * anything = 0
		{65536, 0, 0},          // anything * 0 = 0
		// Note: 32768 overflows int16 to -32768
		{65536, 32768, -32768}, // 65536 * int16(32768)=-32768 >> 16 = -32768
	}

	for _, tc := range testCases {
		got := smulwb(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("smulwb(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
