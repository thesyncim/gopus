package celt

import (
	"math"
	"testing"
)

func TestMDCTForwardOverlapTrailingFoldBoundary(t *testing.T) {
	// Exercise standard overlap (120, overlap%16==8) and all mod-16 classes of
	// valid multiple-of-4 overlaps (112, 116, 108, 32, 28, 24, 20, 16, 12) so
	// SIMD leading/mid/trailing fold guards and exact-zero pointer bounds are
	// exercised under -gcflags=all=-d=checkptr and checked against mdctDirect.
	cases := []struct {
		frameSize int
		overlap   int
	}{
		{frameSize: 960, overlap: 120},
		{frameSize: 480, overlap: 120},
		{frameSize: 240, overlap: 120},
		{frameSize: 120, overlap: 120},
		{frameSize: 960, overlap: 112},
		{frameSize: 240, overlap: 112},
		{frameSize: 240, overlap: 116},
		{frameSize: 240, overlap: 108},
		{frameSize: 240, overlap: 32},
		{frameSize: 240, overlap: 28},
		{frameSize: 240, overlap: 24},
		{frameSize: 240, overlap: 20},
		{frameSize: 240, overlap: 16},
		{frameSize: 240, overlap: 12},
	}

	for _, tc := range cases {
		samples := make([]float32, tc.frameSize+tc.overlap)
		for i := range samples {
			samples[i] = float32(math.Sin(0.13*float64(i)+0.4) + 0.5*math.Cos(0.37*float64(i)))
		}
		got := make([]float32, tc.frameSize)
		mdctForwardOverlapF32Scratch(samples, tc.overlap, got, nil, nil, nil, nil, nil)

		// Build the 2*frameSize zero-padded windowed block and compare against
		// the direct MDCT reference (mdctDirect).
		win := GetWindowBufferF32(tc.overlap)
		n := 2 * tc.frameSize
		pad := (tc.frameSize - tc.overlap) / 2
		padded := make([]float32, n)
		for i := 0; i < tc.overlap; i++ {
			padded[pad+i] = samples[i] * win[i]
			padded[n-pad-1-i] = samples[len(samples)-1-i] * win[i]
		}
		for i := tc.overlap; i < tc.frameSize; i++ {
			padded[pad+i] = samples[i]
		}
		want := mdctDirect(padded)

		maxErr := 0.0
		for k := range got {
			if diff := math.Abs(float64(got[k]) - float64(want[k])); diff > maxErr {
				maxErr = diff
			}
		}
		if maxErr > 1e-4 {
			t.Fatalf("frameSize=%d overlap=%d maxErr=%g > 1e-4", tc.frameSize, tc.overlap, maxErr)
		}
	}
}
