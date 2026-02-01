package silk

import (
	"math"
	"testing"
)

// ============================================================================
// Decoder lifecycle tests
// ============================================================================

func TestDecoderCreation(t *testing.T) {
	d := NewDecoder()
	if d == nil {
		t.Fatal("NewDecoder returned nil")
	}
	if d.haveDecoded {
		t.Error("New decoder should have haveDecoded=false")
	}
	if len(d.prevLPCValues) != 16 {
		t.Errorf("prevLPCValues length = %d, want 16", len(d.prevLPCValues))
	}
	if len(d.prevLSFQ15) != 16 {
		t.Errorf("prevLSFQ15 length = %d, want 16", len(d.prevLSFQ15))
	}
	if len(d.outputHistory) != 322 {
		t.Errorf("outputHistory length = %d, want 322", len(d.outputHistory))
	}
}

func TestDecoderReset(t *testing.T) {
	d := NewDecoder()

	// Set some state
	d.haveDecoded = true
	d.previousLogGain = 12345
	d.isPreviousFrameVoiced = true
	d.lpcOrder = 16
	d.prevLPCValues[0] = 1.0
	d.prevLSFQ15[0] = 1000
	d.outputHistory[0] = 0.5
	d.historyIndex = 100
	d.prevStereoWeights = [2]int16{1000, 2000}

	d.Reset()

	if d.haveDecoded {
		t.Error("Reset did not clear haveDecoded")
	}
	if d.previousLogGain != 0 {
		t.Error("Reset did not clear previousLogGain")
	}
	if d.isPreviousFrameVoiced {
		t.Error("Reset did not clear isPreviousFrameVoiced")
	}
	if d.lpcOrder != 0 {
		t.Error("Reset did not clear lpcOrder")
	}
	if d.prevLPCValues[0] != 0 {
		t.Error("Reset did not clear prevLPCValues")
	}
	if d.prevLSFQ15[0] != 0 {
		t.Error("Reset did not clear prevLSFQ15")
	}
	if d.outputHistory[0] != 0 {
		t.Error("Reset did not clear outputHistory")
	}
	if d.historyIndex != 0 {
		t.Error("Reset did not clear historyIndex")
	}
	if d.prevStereoWeights != [2]int16{0, 0} {
		t.Error("Reset did not clear prevStereoWeights")
	}
}

// ============================================================================
// Resampling tests
// ============================================================================

func TestUpsampleFactors(t *testing.T) {
	tests := []struct {
		bw     Bandwidth
		factor int
	}{
		{BandwidthNarrowband, 6},
		{BandwidthMediumband, 4},
		{BandwidthWideband, 3},
	}

	for _, tt := range tests {
		got := getUpsampleFactor(tt.bw)
		if got != tt.factor {
			t.Errorf("getUpsampleFactor(%v) = %d, want %d", tt.bw, got, tt.factor)
		}
	}
}

func TestUpsampleLength(t *testing.T) {
	// Test all SILK bandwidths
	tests := []struct {
		name      string
		inputLen  int
		srcRate   int
		outputLen int
		bandwidth Bandwidth
	}{
		{"NB 20ms", 160, 8000, 960, BandwidthNarrowband},  // 8kHz 20ms -> 48kHz
		{"MB 20ms", 240, 12000, 960, BandwidthMediumband}, // 12kHz 20ms -> 48kHz
		{"WB 20ms", 320, 16000, 960, BandwidthWideband},   // 16kHz 20ms -> 48kHz
		{"NB 10ms", 80, 8000, 480, BandwidthNarrowband},   // 8kHz 10ms -> 48kHz
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := make([]float32, tt.inputLen)
			for i := range input {
				input[i] = float32(i) / float32(tt.inputLen)
			}

			output := upsampleTo48k(input, tt.srcRate)

			if len(output) != tt.outputLen {
				t.Errorf("output length = %d, want %d", len(output), tt.outputLen)
			}
		})
	}
}

func TestUpsampleMonotonic(t *testing.T) {
	// 160 samples at 8kHz (20ms NB) should become 960 samples at 48kHz
	input := make([]float32, 160)
	for i := range input {
		input[i] = float32(i) / 160.0 // Monotonically increasing ramp
	}

	output := upsampleTo48k(input, 8000)

	if len(output) != 960 {
		t.Errorf("upsampleTo48k output length = %d, want 960", len(output))
	}

	// Check output is monotonically increasing (smooth interpolation)
	// Allow small tolerance for floating point
	for i := 1; i < len(output); i++ {
		if output[i] < output[i-1]-0.001 {
			t.Errorf("Output not monotonic at %d: %f < %f", i, output[i], output[i-1])
			break
		}
	}
}

func TestUpsamplePassthrough(t *testing.T) {
	// 48kHz input should pass through unchanged
	input := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	output := upsampleTo48k(input, 48000)

	if len(output) != len(input) {
		t.Errorf("48kHz passthrough length = %d, want %d", len(output), len(input))
	}
	for i := range input {
		if output[i] != input[i] {
			t.Errorf("output[%d] = %f, want %f", i, output[i], input[i])
		}
	}
}

func TestUpsampleEmpty(t *testing.T) {
	output := upsampleTo48k(nil, 8000)
	if output != nil {
		t.Errorf("Expected nil for empty input, got length %d", len(output))
	}

	output = upsampleTo48k([]float32{}, 8000)
	if output != nil {
		t.Errorf("Expected nil for zero-length input, got length %d", len(output))
	}
}

func TestUpsampleStereo(t *testing.T) {
	left := make([]float32, 160)
	right := make([]float32, 160)
	for i := range left {
		left[i] = float32(i) / 160.0
		right[i] = float32(160-i) / 160.0
	}

	outL, outR := upsampleTo48kStereo(left, right, 8000)

	if len(outL) != 960 {
		t.Errorf("Left channel length = %d, want 960", len(outL))
	}
	if len(outR) != 960 {
		t.Errorf("Right channel length = %d, want 960", len(outR))
	}
}

// ============================================================================
// Bandwidth conversion tests
// ============================================================================

func TestBandwidthFromOpus(t *testing.T) {
	tests := []struct {
		opus int
		silk Bandwidth
		ok   bool
	}{
		{0, BandwidthNarrowband, true},
		{1, BandwidthMediumband, true},
		{2, BandwidthWideband, true},
		{3, 0, false}, // SWB - not SILK
		{4, 0, false}, // FB - not SILK
		{-1, 0, false},
		{10, 0, false},
	}

	for _, tt := range tests {
		bw, ok := BandwidthFromOpus(tt.opus)
		if ok != tt.ok {
			t.Errorf("BandwidthFromOpus(%d) ok = %v, want %v", tt.opus, ok, tt.ok)
		}
		if ok && bw != tt.silk {
			t.Errorf("BandwidthFromOpus(%d) = %v, want %v", tt.opus, bw, tt.silk)
		}
	}
}

// ============================================================================
// API error handling tests
// ============================================================================

func TestDecodeInvalidBandwidth(t *testing.T) {
	d := NewDecoder()

	// Try to decode with invalid bandwidth (SWB = 3)
	_, err := d.Decode([]byte{0, 0, 0, 0}, 3, 960, true)
	if err != ErrInvalidBandwidth {
		t.Errorf("Expected ErrInvalidBandwidth, got %v", err)
	}

	// Try with FB = 4
	_, err = d.Decode([]byte{0, 0, 0, 0}, 4, 960, true)
	if err != ErrInvalidBandwidth {
		t.Errorf("Expected ErrInvalidBandwidth for FB, got %v", err)
	}
}

func TestDecodeStereoInvalidBandwidth(t *testing.T) {
	d := NewDecoder()

	// Try stereo decode with invalid bandwidth
	_, err := d.DecodeStereo([]byte{0, 0, 0, 0}, 3, 960, true)
	if err != ErrInvalidBandwidth {
		t.Errorf("Expected ErrInvalidBandwidth for stereo, got %v", err)
	}
}

// ============================================================================
// int16 conversion tests
// ============================================================================

func TestInt16Conversion(t *testing.T) {
	// Test float32 to int16 conversion
	tests := []struct {
		input    float32
		expected int16
	}{
		{0.0, 0},
		{1.0, 32767},
		{-1.0, -32768},
		{0.5, 16384},
		{-0.5, -16384},
		{2.0, 32767},   // Clamp positive
		{-2.0, -32768}, // Clamp negative
	}

	for _, tt := range tests {
		scaled := float64(tt.input) * 32768.0
		var got int16
		if scaled > 32767.0 {
			got = 32767
		} else if scaled < -32768.0 {
			got = -32768
		} else {
			got = int16(math.RoundToEven(scaled))
		}
		if got != tt.expected {
			t.Errorf("Sample %f -> %d, want %d", tt.input, got, tt.expected)
		}
	}
}

// ============================================================================
// State persistence tests
// ============================================================================

func TestDecoderStatePersistence(t *testing.T) {
	d := NewDecoder()

	if d.haveDecoded {
		t.Error("haveDecoded should be false initially")
	}

	// Simulate decoding a frame by directly setting state
	d.haveDecoded = true
	d.previousLogGain = 100
	d.isPreviousFrameVoiced = true
	d.lpcOrder = 16

	// State should persist
	if !d.haveDecoded {
		t.Error("haveDecoded should persist")
	}
	if d.previousLogGain != 100 {
		t.Error("previousLogGain should persist")
	}
	if !d.isPreviousFrameVoiced {
		t.Error("isPreviousFrameVoiced should persist")
	}
	if d.lpcOrder != 16 {
		t.Error("lpcOrder should persist")
	}

	// Reset should clear state
	d.Reset()
	if d.haveDecoded || d.previousLogGain != 0 || d.isPreviousFrameVoiced || d.lpcOrder != 0 {
		t.Error("Reset should clear all state")
	}
}

func TestDecoderAccessors(t *testing.T) {
	d := NewDecoder()

	// Test accessor methods
	if d.HaveDecoded() {
		t.Error("HaveDecoded() should be false initially")
	}

	d.MarkDecoded()
	if !d.HaveDecoded() {
		t.Error("HaveDecoded() should be true after MarkDecoded()")
	}

	d.SetPreviousLogGain(500)
	if d.PreviousLogGain() != 500 {
		t.Error("PreviousLogGain() mismatch")
	}

	d.SetPreviousFrameVoiced(true)
	if !d.IsPreviousFrameVoiced() {
		t.Error("IsPreviousFrameVoiced() mismatch")
	}

	d.SetLPCOrder(16)
	if d.LPCOrder() != 16 {
		t.Error("LPCOrder() mismatch")
	}

	d.SetHistoryIndex(50)
	if d.HistoryIndex() != 50 {
		t.Error("HistoryIndex() mismatch")
	}

	weights := [2]int16{1000, 2000}
	d.SetPrevStereoWeights(weights)
	if d.PrevStereoWeights() != weights {
		t.Error("PrevStereoWeights() mismatch")
	}
}

// ============================================================================
// Frame duration tests
// ============================================================================

func TestFrameDurationFromTOC(t *testing.T) {
	tests := []struct {
		tocFrameSize int
		expected     FrameDuration
	}{
		{480, Frame10ms},
		{960, Frame20ms},
		{1920, Frame40ms},
		{2880, Frame60ms},
		{0, Frame20ms},    // Default
		{1234, Frame20ms}, // Unknown defaults to 20ms
	}

	for _, tt := range tests {
		got := FrameDurationFromTOC(tt.tocFrameSize)
		if got != tt.expected {
			t.Errorf("FrameDurationFromTOC(%d) = %v, want %v", tt.tocFrameSize, got, tt.expected)
		}
	}
}

// ============================================================================
// Benchmarks
// ============================================================================

func BenchmarkUpsample8to48(b *testing.B) {
	input := make([]float32, 160) // 20ms at 8kHz
	for i := range input {
		input[i] = float32(i) / 160.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = upsampleTo48k(input, 8000)
	}
}

func BenchmarkUpsample12to48(b *testing.B) {
	input := make([]float32, 240) // 20ms at 12kHz
	for i := range input {
		input[i] = float32(i) / 240.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = upsampleTo48k(input, 12000)
	}
}

func BenchmarkUpsample16to48(b *testing.B) {
	input := make([]float32, 320) // 20ms at 16kHz
	for i := range input {
		input[i] = float32(i) / 320.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = upsampleTo48k(input, 16000)
	}
}

func BenchmarkDecoderCreation(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = NewDecoder()
	}
}

func BenchmarkDecoderReset(b *testing.B) {
	d := NewDecoder()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Reset()
	}
}
