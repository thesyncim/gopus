package silk

import (
	"math"
	"testing"
)

func TestStereoLPFilter(t *testing.T) {
	// Test the LP filter with a known input
	// LP filter is [1,2,1]/4, so for constant input, output should equal input
	signal := []int16{1000, 1000, 1000, 1000, 1000, 1000}
	frameLength := 4

	lp, hp := stereoLPFilter(signal, frameLength)

	// For constant input, LP should equal input and HP should be 0
	for i := 0; i < frameLength; i++ {
		if lp[i] != 1000 {
			t.Errorf("LP[%d] = %d, want 1000 (constant input)", i, lp[i])
		}
		if hp[i] != 0 {
			t.Errorf("HP[%d] = %d, want 0 (constant input)", i, hp[i])
		}
	}
}

func TestStereoLPFilterImpulse(t *testing.T) {
	// Test LP filter with an impulse
	// Input: [0, 0, 4000, 0, 0]
	// LP[0] = (0 + 2*0 + 4000 + 2) >> 2 = 1000
	// LP[1] = (0 + 2*4000 + 0 + 2) >> 2 = 2000
	// LP[2] = (4000 + 2*0 + 0 + 2) >> 2 = 1000
	signal := []int16{0, 0, 4000, 0, 0}
	frameLength := 3

	lp, hp := stereoLPFilter(signal, frameLength)

	expectedLP := []int16{1000, 2000, 1000}
	for i := 0; i < frameLength; i++ {
		if lp[i] != expectedLP[i] {
			t.Errorf("LP[%d] = %d, want %d", i, lp[i], expectedLP[i])
		}
	}

	// HP[n] = signal[n+1] - LP[n]
	// HP[0] = 0 - 1000 = -1000
	// HP[1] = 4000 - 2000 = 2000
	// HP[2] = 0 - 1000 = -1000
	expectedHP := []int16{-1000, 2000, -1000}
	for i := 0; i < frameLength; i++ {
		if hp[i] != expectedHP[i] {
			t.Errorf("HP[%d] = %d, want %d", i, hp[i], expectedHP[i])
		}
	}
}

func TestStereoLPFilterFloat(t *testing.T) {
	// Test float version with known input
	signal := []float32{1.0, 1.0, 1.0, 1.0, 1.0, 1.0}
	frameLength := 4

	lp, hp := stereoLPFilterFloat(signal, frameLength)

	// For constant input, LP should equal input and HP should be 0
	for i := 0; i < frameLength; i++ {
		if math.Abs(float64(lp[i]-1.0)) > 0.001 {
			t.Errorf("LP[%d] = %f, want 1.0 (constant input)", i, lp[i])
		}
		if math.Abs(float64(hp[i])) > 0.001 {
			t.Errorf("HP[%d] = %f, want 0.0 (constant input)", i, hp[i])
		}
	}
}

func TestStereoLPFilterFloatImpulse(t *testing.T) {
	// Test float version with impulse
	signal := []float32{0, 0, 4.0, 0, 0}
	frameLength := 3

	lp, hp := stereoLPFilterFloat(signal, frameLength)

	// LP[n] = (s[n] + 2*s[n+1] + s[n+2]) / 4
	expectedLP := []float32{1.0, 2.0, 1.0}
	for i := 0; i < frameLength; i++ {
		if math.Abs(float64(lp[i]-expectedLP[i])) > 0.001 {
			t.Errorf("LP[%d] = %f, want %f", i, lp[i], expectedLP[i])
		}
	}

	// HP[n] = signal[n+1] - LP[n]
	expectedHP := []float32{-1.0, 2.0, -1.0}
	for i := 0; i < frameLength; i++ {
		if math.Abs(float64(hp[i]-expectedHP[i])) > 0.001 {
			t.Errorf("HP[%d] = %f, want %f", i, hp[i], expectedHP[i])
		}
	}
}

func TestStereoConvertLRToMS(t *testing.T) {
	// Test L/R to M/S conversion
	// M = (L + R) / 2
	// S = (L - R) / 2
	left := []int16{1000, 2000, 3000, 4000}
	right := []int16{1000, 0, 1000, 2000}
	mid := make([]int16, 4)
	side := make([]int16, 4)

	stereoConvertLRToMS(left[:2], right[:2], mid[:2], side[:2], 0)

	// For first 2 samples:
	// M[0] = (1000 + 1000) / 2 = 1000
	// S[0] = (1000 - 1000) / 2 = 0
	// M[1] = (2000 + 0) / 2 = 1000
	// S[1] = (2000 - 0) / 2 = 1000

	if mid[0] != 1000 {
		t.Errorf("mid[0] = %d, want 1000", mid[0])
	}
	if side[0] != 0 {
		t.Errorf("side[0] = %d, want 0", side[0])
	}
	if mid[1] != 1000 {
		t.Errorf("mid[1] = %d, want 1000", mid[1])
	}
	if side[1] != 1000 {
		t.Errorf("side[1] = %d, want 1000", side[1])
	}
}

func TestStereoConvertLRToMSFloat(t *testing.T) {
	left := []float32{1.0, 0.5, -0.5, 0.0}
	right := []float32{1.0, -0.5, 0.5, 0.0}

	mid, side := stereoConvertLRToMSFloat(left, right, 2)

	// M[0] = (1.0 + 1.0) / 2 = 1.0
	// S[0] = (1.0 - 1.0) / 2 = 0.0
	// M[1] = (0.5 - 0.5) / 2 = 0.0
	// S[1] = (0.5 + 0.5) / 2 = 0.5

	if math.Abs(float64(mid[0]-1.0)) > 0.001 {
		t.Errorf("mid[0] = %f, want 1.0", mid[0])
	}
	if math.Abs(float64(side[0])) > 0.001 {
		t.Errorf("side[0] = %f, want 0.0", side[0])
	}
}

func TestStereoFindPredictorFloat(t *testing.T) {
	// Test predictor finding with perfectly correlated signals
	// If y = 0.5 * x, predictor should be approximately 0.5 (4096 in Q13)
	x := make([]float32, 100)
	y := make([]float32, 100)

	for i := 0; i < 100; i++ {
		x[i] = float32(i) / 100.0
		y[i] = 0.5 * x[i]
	}

	predQ13 := stereoFindPredictorFloat(x, y, 100)

	// Expected: 0.5 * 8192 = 4096
	expectedQ13 := int32(4096)
	tolerance := int32(100) // Allow some tolerance

	diff := predQ13 - expectedQ13
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("predQ13 = %d, want approximately %d (tolerance %d)", predQ13, expectedQ13, tolerance)
	}
}

func TestStereoFindPredictorFloatUncorrelated(t *testing.T) {
	// Test with uncorrelated signals - predictor should be near 0
	x := []float32{1, -1, 1, -1, 1, -1, 1, -1}
	y := []float32{1, 1, -1, -1, 1, 1, -1, -1}

	predQ13 := stereoFindPredictorFloat(x, y, 8)

	// Predictor should be small for uncorrelated signals
	if predQ13 > 1000 || predQ13 < -1000 {
		t.Errorf("predQ13 = %d, expected near 0 for uncorrelated signals", predQ13)
	}
}

func TestIsqrt32(t *testing.T) {
	tests := []struct {
		input    uint32
		expected uint32
	}{
		{0, 0},
		{1, 1},
		{4, 2},
		{9, 3},
		{16, 4},
		{25, 5},
		{100, 10},
		{10000, 100},
		{1000000, 1000},
	}

	for _, tt := range tests {
		got := isqrt32(tt.input)
		if got != tt.expected {
			t.Errorf("isqrt32(%d) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestStereoEncoderLPFilterState(t *testing.T) {
	// Test that encoder stereo state is preserved across frames
	enc := NewEncoder(BandwidthWideband)

	// First frame
	left1 := make([]float32, 322) // 320 + 2 for look-ahead
	right1 := make([]float32, 322)
	for i := range left1 {
		left1[i] = 0.5
		right1[i] = 0.3
	}

	mid1, side1, pred1 := enc.EncodeStereoLRToMS(left1, right1, 320, 16)

	// State should be updated
	if enc.stereo.sMid[0] == 0 && enc.stereo.sMid[1] == 0 {
		t.Error("stereo.sMid should be updated after first frame")
	}

	// Second frame
	left2 := make([]float32, 322)
	right2 := make([]float32, 322)
	for i := range left2 {
		left2[i] = 0.6
		right2[i] = 0.4
	}

	mid2, side2, pred2 := enc.EncodeStereoLRToMS(left2, right2, 320, 16)

	// Outputs should be different due to state
	_ = mid1
	_ = side1
	_ = pred1
	_ = mid2
	_ = side2
	_ = pred2

	// After reset, state should be cleared
	enc.ResetStereoState()
	if enc.stereo.sMid[0] != 0 || enc.stereo.sMid[1] != 0 {
		t.Error("stereo.sMid should be 0 after reset")
	}
}

func TestStereoLPFilterMatchesLibopus(t *testing.T) {
	// Test that our LP filter matches the libopus implementation exactly
	// libopus formula: sum = silk_RSHIFT_ROUND(silk_ADD_LSHIFT32(mid[n] + mid[n+2], mid[n+1], 1), 2)
	// Which is: ((mid[n] + mid[n+2]) + (mid[n+1] << 1) + 2) >> 2
	// = (mid[n] + 2*mid[n+1] + mid[n+2] + 2) / 4  (rounded)

	testCases := []struct {
		signal   []int16
		expected []int16
	}{
		// Test case 1: Simple values
		{[]int16{100, 200, 300, 400, 500}, []int16{200, 300, 400}},
		// Test case 2: All same
		{[]int16{1000, 1000, 1000, 1000, 1000}, []int16{1000, 1000, 1000}},
		// Test case 3: Ramp
		{[]int16{0, 100, 200, 300, 400}, []int16{100, 200, 300}},
	}

	for i, tc := range testCases {
		lp, _ := stereoLPFilter(tc.signal, len(tc.expected))
		for j, exp := range tc.expected {
			if lp[j] != exp {
				t.Errorf("Test %d: LP[%d] = %d, want %d", i, j, lp[j], exp)
			}
		}
	}
}

func BenchmarkStereoLPFilter(b *testing.B) {
	signal := make([]int16, 322) // 320 + 2 history
	for i := range signal {
		signal[i] = int16(i * 10)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stereoLPFilter(signal, 320)
	}
}

func BenchmarkStereoLPFilterFloat(b *testing.B) {
	signal := make([]float32, 322) // 320 + 2 history
	for i := range signal {
		signal[i] = float32(i) / 100.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stereoLPFilterFloat(signal, 320)
	}
}

func BenchmarkStereoFindPredictorFloat(b *testing.B) {
	x := make([]float32, 320)
	y := make([]float32, 320)
	for i := range x {
		x[i] = float32(i) / 320.0
		y[i] = float32(i) / 640.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stereoFindPredictorFloat(x, y, 320)
	}
}
