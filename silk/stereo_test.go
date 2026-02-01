package silk

import (
	"math"
	"testing"
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
	// Verify stereo prediction weight table is symmetric
	for i := 0; i < 4; i++ {
		// Weights should be symmetric around midpoint
		// stereoPredWeights[i] should relate to stereoPredWeights[7-i]
		if stereoPredWeights[i] != -stereoPredWeights[7-i] {
			t.Errorf("stereoPredWeights[%d] = %d should be negative of stereoPredWeights[%d] = %d",
				i, stereoPredWeights[i], 7-i, stereoPredWeights[7-i])
		}
	}
}
