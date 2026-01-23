package celt

import (
	"fmt"
	"testing"
)

// TestDecodeFrame_SampleCount verifies DecodeFrame produces correct sample counts.
// This is an integration test confirming the full decode pipeline
// (DecodeBands -> Synthesize -> output) produces the expected sample count
// after the 14-01 (MDCT bin count) and 14-02 (overlap-add) fixes.
func TestDecodeFrame_SampleCount(t *testing.T) {
	testCases := []struct {
		frameSize       int
		expectedSamples int // After 14-02 fix: exactly frameSize samples
	}{
		{120, 120},   // 2.5ms: 120 samples
		{240, 240},   // 5ms: 240 samples
		{480, 480},   // 10ms: 480 samples
		{960, 960},   // 20ms: 960 samples
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
// After 14-02 fix, stereo produces 2*frameSize interleaved samples.
func TestDecodeFrame_SampleCount_Stereo(t *testing.T) {
	testCases := []struct {
		frameSize       int
		expectedSamples int // Stereo interleaved: 2 * frameSize
	}{
		{120, 240},   // 2.5ms: 2 * 120 = 240
		{240, 480},   // 5ms: 2 * 240 = 480
		{480, 960},   // 10ms: 2 * 480 = 960
		{960, 1920},  // 20ms: 2 * 960 = 1920
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
// After 14-02 fix, DecodeFrame consistently returns frameSize samples.
func TestDecodeFrame_ConsecutiveFrames(t *testing.T) {
	d := NewDecoder(1)
	d.Reset() // Ensure clean state
	frameSize := 960

	// Decode multiple consecutive frames (PLC mode)
	for i := 0; i < 5; i++ {
		samples, err := d.DecodeFrame(nil, frameSize)
		if err != nil {
			t.Fatalf("Frame %d: DecodeFrame error: %v", i, err)
		}

		// After 14-02 fix, output is consistently frameSize samples
		if len(samples) != frameSize {
			t.Errorf("Frame %d: got %d samples, want %d", i, len(samples), frameSize)
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

// TestDecodeFrame_ShortFrames verifies CELT can decode 2.5ms and 5ms frame sizes.
// RFC 8251 test vectors include short frames. This test confirms the decoder
// handles them correctly with proper sample output counts.
func TestDecodeFrame_ShortFrames(t *testing.T) {
	// Test 2.5ms and 5ms frames with actual CELT data
	testCases := []struct {
		name      string
		frameSize int
		wantLen   int // Expected output sample count
	}{
		{"2.5ms_mono", 120, 120},
		{"5ms_mono", 240, 240},
		{"10ms_mono", 480, 480},
		{"20ms_mono", 960, 960},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecoder(1)

			// Create a minimal valid CELT frame
			// Silence frame is simplest: first bit = 1 (probability 15/32 = 0x80)
			silenceFrame := []byte{0x80} // Silence flag set

			samples, err := d.DecodeFrame(silenceFrame, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFrame failed: %v", err)
			}

			// Verify we get exactly frameSize samples
			if len(samples) != tc.wantLen {
				t.Errorf("got %d samples, want %d", len(samples), tc.wantLen)
			}
		})
	}
}

// TestDecodeFrame_ShortFrameStereo verifies stereo short frame decoding.
func TestDecodeFrame_ShortFrameStereo(t *testing.T) {
	testCases := []struct {
		frameSize int
		wantLen   int // samples per channel * 2 (interleaved)
	}{
		{120, 240}, // 2.5ms stereo: 120*2
		{240, 480}, // 5ms stereo: 240*2
		{480, 960}, // 10ms stereo: 480*2
		{960, 1920}, // 20ms stereo: 960*2
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%d_stereo", tc.frameSize), func(t *testing.T) {
			d := NewDecoder(2)
			silenceFrame := []byte{0x80}

			samples, err := d.DecodeFrame(silenceFrame, tc.frameSize)
			if err != nil {
				t.Fatalf("DecodeFrame failed: %v", err)
			}

			if len(samples) != tc.wantLen {
				t.Errorf("got %d samples, want %d", len(samples), tc.wantLen)
			}
		})
	}
}

// TestDecodeFrame_ShortFrameConsecutive verifies consecutive short frame decoding
// produces consistent sample counts with proper overlap-add state maintenance.
func TestDecodeFrame_ShortFrameConsecutive(t *testing.T) {
	testCases := []int{120, 240} // 2.5ms and 5ms

	for _, frameSize := range testCases {
		t.Run(fmt.Sprintf("%d", frameSize), func(t *testing.T) {
			d := NewDecoder(1)
			silenceFrame := []byte{0x80}

			// Decode 5 consecutive frames
			for i := 0; i < 5; i++ {
				samples, err := d.DecodeFrame(silenceFrame, frameSize)
				if err != nil {
					t.Fatalf("Frame %d: DecodeFrame failed: %v", i, err)
				}

				if len(samples) != frameSize {
					t.Errorf("Frame %d: got %d samples, want %d", i, len(samples), frameSize)
				}
			}
		})
	}
}
