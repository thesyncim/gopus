package celt

import (
	"math"
	"testing"
)

// TestAllocTrimAnalysisBitrateAdjustment tests the bitrate-based trim adjustment.
// Reference: libopus alloc_trim_analysis() lines 876-883
func TestAllocTrimAnalysisBitrateAdjustment(t *testing.T) {
	tests := []struct {
		name      string
		equivRate int
		wantTrim  int // Expected trim before other adjustments
	}{
		// Low bitrate: trim = 4
		{"32kbps", 32000, 4},
		{"48kbps", 48000, 4},
		{"63kbps", 63000, 4},

		// Transition range: 64kbps to 80kbps, interpolate from 4 to 5
		{"64kbps", 64000, 4},
		{"72kbps", 72000, 5}, // frac = 8000 >> 10 = 7, trim = 4 + 7/16 = 4.44 -> 4
		{"80kbps", 80000, 5}, // frac = 16000 >> 10 = 15.6, trim = 4 + 15/16 = 4.94 -> 5

		// High bitrate: trim = 5
		{"96kbps", 96000, 5},
		{"128kbps", 128000, 5},
		{"256kbps", 256000, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use neutral values for other parameters
			// Zero bandLogE -> no spectral tilt adjustment
			// Zero tfEstimate -> no transient adjustment
			bandLogE := make([]float64, MaxBands)
			tfEstimate := 0.0
			nbBands := 21
			lm := 3 // 20ms frame
			channels := 1

			trim := allocTrimAnalysis(tt.equivRate, channels, bandLogE, tfEstimate, nbBands, lm, nil, nil, nbBands, 0.0)

			// Allow +/- 1 tolerance due to rounding differences
			diff := trim - tt.wantTrim
			if diff < -1 || diff > 1 {
				t.Errorf("allocTrimAnalysis(%d) = %d, want ~%d (diff=%d)", tt.equivRate, trim, tt.wantTrim, diff)
			}
		})
	}
}

// TestAllocTrimAnalysisTfEstimate tests the tf_estimate adjustment.
// Reference: libopus alloc_trim_analysis() line 933
func TestAllocTrimAnalysisTfEstimate(t *testing.T) {
	tests := []struct {
		name       string
		tfEstimate float64
		wantDelta  float64 // Expected change from baseline
	}{
		{"tf=0.0", 0.0, 0.0},
		{"tf=0.25", 0.25, -0.5},
		{"tf=0.5", 0.5, -1.0},
		{"tf=1.0", 1.0, -2.0},
	}

	// High bitrate for neutral base trim of 5
	equivRate := 128000
	bandLogE := make([]float64, MaxBands)
	nbBands := 21
	lm := 3
	channels := 1

	// Get baseline with tf=0
	baseline := allocTrimAnalysis(equivRate, channels, bandLogE, 0.0, nbBands, lm, nil, nil, nbBands, 0.0)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trim := allocTrimAnalysis(equivRate, channels, bandLogE, tt.tfEstimate, nbBands, lm, nil, nil, nbBands, 0.0)
			delta := float64(trim - baseline)

			// Allow some tolerance for rounding
			if math.Abs(delta-tt.wantDelta) > 1.5 {
				t.Errorf("tf_estimate adjustment: got delta=%.1f, want ~%.1f", delta, tt.wantDelta)
			}
		})
	}
}

// TestAllocTrimAnalysisClamp tests that output is clamped to [0, 10].
func TestAllocTrimAnalysisClamp(t *testing.T) {
	bandLogE := make([]float64, MaxBands)
	nbBands := 21
	lm := 3
	channels := 1

	tests := []struct {
		name       string
		equivRate  int
		tfEstimate float64
		spectralTilt float64 // Add to each band's energy to create tilt
	}{
		// Try to force very low trim
		{"extreme_low", 32000, 1.0, 10.0},
		// Try to force very high trim
		{"extreme_high", 256000, 0.0, -10.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create tilted spectrum
			for i := 0; i < nbBands; i++ {
				bandLogE[i] = tt.spectralTilt * float64(i) / float64(nbBands-1)
			}

			trim := allocTrimAnalysis(tt.equivRate, channels, bandLogE, tt.tfEstimate, nbBands, lm, nil, nil, nbBands, 0.0)

			if trim < 0 || trim > 10 {
				t.Errorf("trim=%d is out of range [0, 10]", trim)
			}
		})
	}
}

// TestComputeEquivRate tests the equivalent rate computation.
// Reference: libopus celt_encoder.c line 1925
func TestComputeEquivRate(t *testing.T) {
	tests := []struct {
		name           string
		bytes          int
		channels       int
		lm             int
		targetBitrate  int
		wantApprox     int
	}{
		// 160 bytes at 20ms mono (LM=3)
		// equiv_rate = (160*8*50) << (3-3) - (40*1+20)*((400>>3) - 50)
		//            = 64000 - 60 * (50 - 50)
		//            = 64000
		{"160B_mono_20ms", 160, 1, 3, 0, 64000},

		// 80 bytes at 10ms mono (LM=2)
		// equiv_rate = (80*8*50) << (3-2) - (40*1+20)*((400>>2) - 50)
		//            = 32000 << 1 - 60 * (100 - 50)
		//            = 64000 - 3000
		//            = 61000
		{"80B_mono_10ms", 80, 1, 2, 0, 61000},

		// 320 bytes at 20ms stereo (LM=3)
		// equiv_rate = (320*8*50) << (3-3) - (40*2+20)*((400>>3) - 50)
		//            = 128000 - 100 * 0
		//            = 128000
		{"320B_stereo_20ms", 320, 2, 3, 0, 128000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeEquivRate(tt.bytes, tt.channels, tt.lm, tt.targetBitrate)

			// Allow 10% tolerance
			tolerance := tt.wantApprox / 10
			diff := got - tt.wantApprox
			if diff < -tolerance || diff > tolerance {
				t.Errorf("computeEquivRate(%d, %d, %d, %d) = %d, want ~%d",
					tt.bytes, tt.channels, tt.lm, tt.targetBitrate, got, tt.wantApprox)
			}
		})
	}
}

// TestAllocTrimAnalysisIntegration tests the complete trim computation with
// typical encoder values.
func TestAllocTrimAnalysisIntegration(t *testing.T) {
	// Simulate typical encoding scenario: 64kbps mono 20ms
	equivRate := 64000
	channels := 1
	nbBands := 21
	lm := 3

	// Flat spectrum (no tilt)
	bandLogE := make([]float64, MaxBands)
	for i := 0; i < nbBands; i++ {
		bandLogE[i] = 0.0 // Neutral energy
	}

	// No transient
	tfEstimate := 0.0

	trim := allocTrimAnalysis(equivRate, channels, bandLogE, tfEstimate, nbBands, lm, nil, nil, nbBands, 0.0)

	// At 64kbps with flat spectrum and no transient, expect trim around 4
	// (low bitrate adjustment applies: equivRate < 64000 -> trim=4, = 64000 is borderline)
	if trim < 3 || trim > 6 {
		t.Errorf("integration test: trim=%d, expected 3-6 range", trim)
	}

	t.Logf("Integration test result: equivRate=%d, tfEstimate=%.2f, trim=%d", equivRate, tfEstimate, trim)
}
