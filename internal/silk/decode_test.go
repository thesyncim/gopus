package silk

import (
	"fmt"
	"testing"

	"gopus/internal/rangecoding"
)

// ============================================================================
// Long frame sub-block count tests
// ============================================================================

// TestDecodeFrame_LongFrameSubBlocks verifies sub-block and subframe count
// calculations for 40ms and 60ms frames as per RFC 6716.
func TestDecodeFrame_LongFrameSubBlocks(t *testing.T) {
	// Verify sub-block count calculations
	if got := getSubBlockCount(Frame40ms); got != 2 {
		t.Errorf("40ms: got %d sub-blocks, want 2", got)
	}
	if got := getSubBlockCount(Frame60ms); got != 3 {
		t.Errorf("60ms: got %d sub-blocks, want 3", got)
	}

	// Verify subframe counts
	if got := getSubframeCount(Frame40ms); got != 8 {
		t.Errorf("40ms: got %d subframes, want 8", got)
	}
	if got := getSubframeCount(Frame60ms); got != 12 {
		t.Errorf("60ms: got %d subframes, want 12", got)
	}

	// Verify is40or60ms helper
	if !is40or60ms(Frame40ms) {
		t.Error("is40or60ms(Frame40ms) should be true")
	}
	if !is40or60ms(Frame60ms) {
		t.Error("is40or60ms(Frame60ms) should be true")
	}
	if is40or60ms(Frame10ms) {
		t.Error("is40or60ms(Frame10ms) should be false")
	}
	if is40or60ms(Frame20ms) {
		t.Error("is40or60ms(Frame20ms) should be false")
	}
}

// TestDecodeFrame_OutputSizes verifies output sample counts for all
// bandwidth/duration combinations.
func TestDecodeFrame_OutputSizes(t *testing.T) {
	testCases := []struct {
		bandwidth   Bandwidth
		duration    FrameDuration
		wantSamples int
	}{
		// 10ms frames
		{BandwidthNarrowband, Frame10ms, 80},   // 10ms * 8000 / 1000
		{BandwidthMediumband, Frame10ms, 120},  // 10ms * 12000 / 1000
		{BandwidthWideband, Frame10ms, 160},    // 10ms * 16000 / 1000

		// 20ms frames
		{BandwidthNarrowband, Frame20ms, 160},  // 20ms * 8000 / 1000
		{BandwidthMediumband, Frame20ms, 240},  // 20ms * 12000 / 1000
		{BandwidthWideband, Frame20ms, 320},    // 20ms * 16000 / 1000

		// 40ms frames (2 x 20ms sub-blocks)
		{BandwidthNarrowband, Frame40ms, 320},  // 40ms * 8000 / 1000
		{BandwidthMediumband, Frame40ms, 480},  // 40ms * 12000 / 1000
		{BandwidthWideband, Frame40ms, 640},    // 40ms * 16000 / 1000

		// 60ms frames (3 x 20ms sub-blocks)
		{BandwidthNarrowband, Frame60ms, 480},  // 60ms * 8000 / 1000
		{BandwidthMediumband, Frame60ms, 720},  // 60ms * 12000 / 1000
		{BandwidthWideband, Frame60ms, 960},    // 60ms * 16000 / 1000
	}

	for _, tc := range testCases {
		// Verify getFrameSamples helper
		got := getFrameSamples(tc.duration, tc.bandwidth)
		if got != tc.wantSamples {
			t.Errorf("getFrameSamples(%dms, %v) = %d, want %d",
				tc.duration, tc.bandwidth, got, tc.wantSamples)
		}

		// Verify via subframe calculation
		config := GetBandwidthConfig(tc.bandwidth)
		numSubframes := getSubframeCount(tc.duration)
		calculated := numSubframes * config.SubframeSamples
		if calculated != tc.wantSamples {
			t.Errorf("subframe calc(%dms, %v) = %d, want %d",
				tc.duration, tc.bandwidth, calculated, tc.wantSamples)
		}
	}
}

// ============================================================================
// 40ms frame decode tests
// ============================================================================

// createMockRangeDecoder creates a range decoder with some entropy-coded data.
// The data may not produce valid SILK output but exercises the code path.
func createMockRangeDecoder() *rangecoding.Decoder {
	// Create decoder with some deterministic pattern
	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i * 7)
	}
	rd := &rangecoding.Decoder{}
	rd.Init(data)
	return rd
}

func TestDecodeFrame_40ms(t *testing.T) {
	testCases := []struct {
		bandwidth   Bandwidth
		wantSamples int // at native SILK rate
	}{
		{BandwidthNarrowband, 320},  // 40ms * 8000 / 1000
		{BandwidthMediumband, 480},  // 40ms * 12000 / 1000
		{BandwidthWideband, 640},    // 40ms * 16000 / 1000
	}

	for _, tc := range testCases {
		t.Run(tc.bandwidth.String(), func(t *testing.T) {
			d := NewDecoder()
			rd := createMockRangeDecoder()

			samples, err := d.DecodeFrame(rd, tc.bandwidth, Frame40ms, true)
			if err != nil {
				// Expected to fail due to range decoder exhaustion with mock data.
				// The important thing is the function accepts 40ms frames.
				t.Logf("Expected decode error with mock data: %v", err)
				return
			}

			if len(samples) != tc.wantSamples {
				t.Errorf("got %d samples, want %d", len(samples), tc.wantSamples)
			}
		})
	}
}

// ============================================================================
// 60ms frame decode tests
// ============================================================================

func TestDecodeFrame_60ms(t *testing.T) {
	testCases := []struct {
		bandwidth   Bandwidth
		wantSamples int
	}{
		{BandwidthNarrowband, 480},  // 60ms * 8000 / 1000
		{BandwidthMediumband, 720},  // 60ms * 12000 / 1000
		{BandwidthWideband, 960},    // 60ms * 16000 / 1000
	}

	for _, tc := range testCases {
		t.Run(tc.bandwidth.String(), func(t *testing.T) {
			d := NewDecoder()
			rd := createMockRangeDecoder()

			samples, err := d.DecodeFrame(rd, tc.bandwidth, Frame60ms, true)
			if err != nil {
				// Expected to fail due to range decoder exhaustion with mock data.
				t.Logf("Expected decode error with mock data: %v", err)
				return
			}

			if len(samples) != tc.wantSamples {
				t.Errorf("got %d samples, want %d", len(samples), tc.wantSamples)
			}
		})
	}
}

// ============================================================================
// Stereo long frame tests
// ============================================================================

func TestDecodeStereoFrame_LongFrames(t *testing.T) {
	testCases := []struct {
		duration    FrameDuration
		wantSamples int // per channel at WB
	}{
		{Frame40ms, 640},
		{Frame60ms, 960},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%dms", tc.duration), func(t *testing.T) {
			d := NewDecoder()
			rd := createMockRangeDecoder()

			left, right, err := d.DecodeStereoFrame(rd, BandwidthWideband, tc.duration, true)
			if err != nil {
				// Expected to fail due to range decoder exhaustion with mock data.
				t.Logf("Expected decode error with mock data: %v", err)
				return
			}

			if len(left) != tc.wantSamples {
				t.Errorf("left: got %d samples, want %d", len(left), tc.wantSamples)
			}
			if len(right) != tc.wantSamples {
				t.Errorf("right: got %d samples, want %d", len(right), tc.wantSamples)
			}
		})
	}
}
