package celt

import (
	"fmt"
	"testing"
)

// TestSynthesize_SampleCount verifies Synthesize produces correct sample counts.
// This test confirms the fix for MDCT bin count mismatch (14-01) and
// overlap-add fix (14-02). With frameSize coefficients, IMDCT produces
// 2*frameSize samples, and overlap-add yields frameSize output samples.
func TestSynthesize_SampleCount(t *testing.T) {
	testCases := []int{120, 240, 480, 960}

	for _, frameSize := range testCases {
		t.Run(fmt.Sprintf("%d", frameSize), func(t *testing.T) {
			d := NewDecoder(1)

			// Create frameSize coefficients (as DecodeBands returns)
			coeffs := make([]float64, frameSize)

			// IMDCT produces 2*frameSize samples
			// After 14-02 fix, OverlapAdd produces frameSize samples
			samples := d.Synthesize(coeffs, false, 1)

			// Output should be exactly frameSize samples
			if len(samples) != frameSize {
				t.Errorf("First frame: got %d samples, want %d", len(samples), frameSize)
			}

			// Second frame should also produce frameSize samples
			samples2 := d.Synthesize(coeffs, false, 1)
			if len(samples2) != frameSize {
				t.Errorf("Second frame: got %d samples, want %d", len(samples2), frameSize)
			}

			t.Logf("frameSize=%d: IMDCT produces %d samples, overlap-add yields %d samples",
				frameSize, 2*frameSize, frameSize)
		})
	}
}

// TestSynthesizeStereo_SampleCount verifies stereo synthesis produces correct sample counts.
// After 14-02 fix, stereo synthesis produces 2*frameSize interleaved samples.
func TestSynthesizeStereo_SampleCount(t *testing.T) {
	testCases := []int{120, 240, 480, 960}

	for _, frameSize := range testCases {
		t.Run(fmt.Sprintf("%d", frameSize), func(t *testing.T) {
			d := NewDecoder(2) // Stereo

			// Create frameSize coefficients per channel
			coeffsL := make([]float64, frameSize)
			coeffsR := make([]float64, frameSize)

			// Call SynthesizeStereo
			samples := d.SynthesizeStereo(coeffsL, coeffsR, false, 1)

			// Stereo output is interleaved [L0, R0, L1, R1, ...]
			// Per channel: frameSize samples (after 14-02 fix)
			// Interleaved: 2 * frameSize samples
			expectedStereo := frameSize * 2

			if len(samples) != expectedStereo {
				t.Errorf("Stereo synthesis: got %d samples, want %d", len(samples), expectedStereo)
			}
		})
	}
}

// TestOverlapAdd_Properties verifies overlap-add produces correct output length.
// After the 14-02 fix, OverlapAdd produces frameSize = inputLen/2 samples.
func TestOverlapAdd_Properties(t *testing.T) {
	testCases := []struct {
		name        string
		inputLen    int  // IMDCT output: 2*frameSize
		overlap     int
		expectedOut int  // frameSize = inputLen/2
	}{
		{"standard 960", 1920, 120, 960},  // 20ms: 1920/2 = 960
		{"standard 480", 960, 120, 480},   // 10ms: 960/2 = 480
		{"standard 240", 480, 120, 240},   // 5ms: 480/2 = 240
		{"standard 120", 240, 120, 120},   // 2.5ms: 240/2 = 120
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			current := make([]float64, tc.inputLen)
			prevOverlap := make([]float64, tc.overlap)

			output, newOverlap := OverlapAdd(current, prevOverlap, tc.overlap)

			if len(output) != tc.expectedOut {
				t.Errorf("OverlapAdd output len = %d, want %d", len(output), tc.expectedOut)
			}

			if len(newOverlap) != tc.overlap {
				t.Errorf("OverlapAdd newOverlap len = %d, want %d", len(newOverlap), tc.overlap)
			}
		})
	}
}

// TestOverlapAdd_OutputSize verifies OverlapAdd produces frameSize samples for all frame sizes.
// This is the key test for the 14-02 fix: IMDCT of N coefficients produces 2N samples,
// and OverlapAdd should produce exactly N output samples (frameSize).
func TestOverlapAdd_OutputSize(t *testing.T) {
	testCases := []int{120, 240, 480, 960}
	overlap := 120

	for _, frameSize := range testCases {
		t.Run(fmt.Sprintf("%d", frameSize), func(t *testing.T) {
			imdctOut := make([]float64, 2*frameSize)
			prevOverlap := make([]float64, overlap)

			output, newOverlap := OverlapAdd(imdctOut, prevOverlap, overlap)

			if len(output) != frameSize {
				t.Errorf("frameSize %d: got output %d, want %d", frameSize, len(output), frameSize)
			}
			if len(newOverlap) != overlap {
				t.Errorf("frameSize %d: got newOverlap %d, want %d", frameSize, len(newOverlap), overlap)
			}
		})
	}
}

// TestIMDCT_OutputSize verifies IMDCT produces 2*N samples from N coefficients.
func TestIMDCT_OutputSize(t *testing.T) {
	testCases := []int{120, 240, 480, 960}

	for _, n := range testCases {
		t.Run(fmt.Sprintf("%d", n), func(t *testing.T) {
			coeffs := make([]float64, n)

			output := IMDCT(coeffs)

			expectedLen := 2 * n
			if len(output) != expectedLen {
				t.Errorf("IMDCT(%d coeffs) produced %d samples, want %d", n, len(output), expectedLen)
			}
		})
	}
}
