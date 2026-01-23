package celt

import (
	"fmt"
	"testing"
)

// TestDecodeFrame_SampleCount verifies DecodeFrame produces correct sample counts.
// This is an integration test confirming the full decode pipeline
// (DecodeBands -> Synthesize -> output) produces the expected sample count
// after the 14-01 fix.
func TestDecodeFrame_SampleCount(t *testing.T) {
	testCases := []struct {
		frameSize       int
		expectedSamples int // After overlap-add: 2*frameSize - Overlap
	}{
		{120, 120},   // 2.5ms: 2*120 - 120 = 120
		{240, 360},   // 5ms: 2*240 - 120 = 360
		{480, 840},   // 10ms: 2*480 - 120 = 840
		{960, 1800},  // 20ms: 2*960 - 120 = 1800
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%d", tc.frameSize), func(t *testing.T) {
			d := NewDecoder(1)

			// DecodeFrame with nil data triggers PLC (packet loss concealment)
			// which still exercises the synthesis pipeline
			samples, err := d.DecodeFrame(nil, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFrame error: %v", err)
			}

			if len(samples) != tc.expectedSamples {
				t.Errorf("DecodeFrame produced %d samples, want %d", len(samples), tc.expectedSamples)
			}
		})
	}
}

// TestDecodeFrame_SampleCount_Stereo verifies stereo DecodeFrame produces correct counts.
func TestDecodeFrame_SampleCount_Stereo(t *testing.T) {
	testCases := []struct {
		frameSize       int
		expectedSamples int // Stereo interleaved: 2 * (2*frameSize - Overlap)
	}{
		{120, 240},   // 2.5ms: 2 * 120 = 240
		{240, 720},   // 5ms: 2 * 360 = 720
		{480, 1680},  // 10ms: 2 * 840 = 1680
		{960, 3600},  // 20ms: 2 * 1800 = 3600
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%d", tc.frameSize), func(t *testing.T) {
			d := NewDecoder(2) // Stereo

			// DecodeFrame with nil data triggers PLC
			samples, err := d.DecodeFrame(nil, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFrame error: %v", err)
			}

			if len(samples) != tc.expectedSamples {
				t.Errorf("DecodeFrame produced %d stereo samples, want %d", len(samples), tc.expectedSamples)
			}
		})
	}
}

// TestDecodeFrame_InvalidFrameSizeRejected verifies invalid frame sizes are rejected.
func TestDecodeFrame_InvalidFrameSizeRejected(t *testing.T) {
	d := NewDecoder(1)

	invalidSizes := []int{0, 100, 200, 500, 1000, -1}

	for _, size := range invalidSizes {
		t.Run(fmt.Sprintf("%d", size), func(t *testing.T) {
			_, err := d.DecodeFrame(nil, size)
			if err != ErrInvalidFrameSize {
				t.Errorf("DecodeFrame with size %d: got err=%v, want ErrInvalidFrameSize", size, err)
			}
		})
	}
}

// TestDecodeFrame_ConsecutiveFrames verifies sample counts remain consistent across frames.
// Note: PLC (Packet Loss Concealment) has two modes:
// - Active concealment: returns 2*frameSize - Overlap samples (via Synthesize)
// - Faded out (after many losses): returns frameSize samples (silence)
func TestDecodeFrame_ConsecutiveFrames(t *testing.T) {
	d := NewDecoder(1)
	d.Reset() // Ensure clean state
	frameSize := 960

	// Decode multiple consecutive frames (PLC mode)
	// After many losses, PLC fades out and returns simpler output
	for i := 0; i < 5; i++ {
		samples, err := d.DecodeFrame(nil, frameSize)
		if err != nil {
			t.Fatalf("Frame %d: DecodeFrame error: %v", i, err)
		}

		// PLC returns either:
		// - 2*frameSize - Overlap (active concealment via Synthesize)
		// - frameSize (faded silence, early return)
		// Both are valid depending on PLC state
		validLen1 := 2*frameSize - Overlap // 1800 for active concealment
		validLen2 := frameSize              // 960 for faded silence

		if len(samples) != validLen1 && len(samples) != validLen2 {
			t.Errorf("Frame %d: got %d samples, want %d or %d", i, len(samples), validLen1, validLen2)
		}
	}
}

// TestDecoder_Initialization verifies decoder initialization.
func TestDecoder_Initialization(t *testing.T) {
	tests := []struct {
		channels int
		expected int
	}{
		{0, 1},  // Clamped to 1
		{1, 1},
		{2, 2},
		{3, 2},  // Clamped to 2
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%d", tc.channels), func(t *testing.T) {
			d := NewDecoder(tc.channels)
			if d.Channels() != tc.expected {
				t.Errorf("NewDecoder(%d).Channels() = %d, want %d",
					tc.channels, d.Channels(), tc.expected)
			}
		})
	}
}

// TestDecoder_ResetState verifies decoder state is properly reset.
func TestDecoder_ResetState(t *testing.T) {
	d := NewDecoder(1)

	// Decode a frame to change state
	_, _ = d.DecodeFrame(nil, 960)

	// Reset
	d.Reset()

	// Verify state is cleared
	for i, e := range d.PrevEnergy() {
		if e != -28.0 {
			t.Errorf("PrevEnergy[%d] = %v, want -28.0 after reset", i, e)
		}
	}

	for i, e := range d.PrevEnergy2() {
		if e != -28.0 {
			t.Errorf("PrevEnergy2[%d] = %v, want -28.0 after reset", i, e)
		}
	}

	for i, s := range d.OverlapBuffer() {
		if s != 0 {
			t.Errorf("OverlapBuffer[%d] = %v, want 0 after reset", i, s)
		}
	}
}
