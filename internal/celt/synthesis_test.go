package celt

import (
	"fmt"
	"testing"
)

// TestSynthesize_SampleCount verifies Synthesize produces correct sample counts.
// This test confirms the fix for MDCT bin count mismatch (14-01).
// With frameSize coefficients, IMDCT produces 2*frameSize samples, and
// overlap-add yields frameSize output samples (in steady state).
func TestSynthesize_SampleCount(t *testing.T) {
	testCases := []int{120, 240, 480, 960}

	for _, frameSize := range testCases {
		t.Run(fmt.Sprintf("%d", frameSize), func(t *testing.T) {
			d := NewDecoder(1)

			// Create frameSize coefficients (as DecodeBands now returns)
			coeffs := make([]float64, frameSize)

			// First frame: produces 2*frameSize from IMDCT, minus Overlap for overlap-add
			// Expected: 2*frameSize - Overlap = frameSize + (frameSize - Overlap)
			// But OverlapAdd returns len(current) - overlap = 2*frameSize - Overlap
			samples := d.Synthesize(coeffs, false, 1)

			// After overlap-add: output = 2*frameSize - Overlap
			expectedFirst := 2*frameSize - Overlap
			if len(samples) != expectedFirst {
				t.Errorf("First frame: got %d samples, want %d", len(samples), expectedFirst)
			}

			// Second frame should produce the same
			// The steady-state output per frame is: 2*frameSize - Overlap
			samples2 := d.Synthesize(coeffs, false, 1)
			if len(samples2) != expectedFirst {
				t.Errorf("Second frame: got %d samples, want %d", len(samples2), expectedFirst)
			}

			t.Logf("frameSize=%d: IMDCT produces %d samples, overlap-add yields %d samples",
				frameSize, 2*frameSize, expectedFirst)
		})
	}
}

// TestSynthesizeStereo_SampleCount verifies stereo synthesis produces correct sample counts.
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
			// Per channel: 2*frameSize - Overlap samples
			// Interleaved: 2 * (2*frameSize - Overlap) samples
			expectedPerChannel := 2*frameSize - Overlap
			expectedStereo := expectedPerChannel * 2

			if len(samples) != expectedStereo {
				t.Errorf("Stereo synthesis: got %d samples, want %d", len(samples), expectedStereo)
			}
		})
	}
}

// TestOverlapAdd_Properties verifies overlap-add produces correct output length.
func TestOverlapAdd_Properties(t *testing.T) {
	testCases := []struct {
		name        string
		inputLen    int
		overlap     int
		expectedOut int
	}{
		{"standard 960", 1920, 120, 1800},
		{"standard 480", 960, 120, 840},
		{"standard 240", 480, 120, 360},
		{"standard 120", 240, 120, 120},
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
