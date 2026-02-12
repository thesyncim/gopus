package celt

import (
	"testing"
)

func TestAllocTrimAnalysis(t *testing.T) {
	// Test cases for allocation trim analysis
	tests := []struct {
		name          string
		equivRate     int
		channels      int
		tfEstimate    float64
		expectMinTrim int
		expectMaxTrim int
		description   string
	}{
		{
			name:          "Low bitrate mono",
			equivRate:     32000,
			channels:      1,
			tfEstimate:    0.0,
			expectMinTrim: 3,
			expectMaxTrim: 5,
			description:   "At low bitrate, trim should be reduced (around 4)",
		},
		{
			name:          "High bitrate mono",
			equivRate:     128000,
			channels:      1,
			tfEstimate:    0.0,
			expectMinTrim: 4,
			expectMaxTrim: 6,
			description:   "At high bitrate, trim should be near default (5)",
		},
		{
			name:          "Medium bitrate with high TF",
			equivRate:     64000,
			channels:      1,
			tfEstimate:    0.8,
			expectMinTrim: 2,
			expectMaxTrim: 4,
			description:   "High TF estimate should reduce trim (transient signal)",
		},
		{
			name:          "Transition bitrate (72kbps)",
			equivRate:     72000,
			channels:      1,
			tfEstimate:    0.0,
			expectMinTrim: 4,
			expectMaxTrim: 5,
			description:   "Between 64k and 80k, trim interpolates from 4 to 5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create simple normalized coefficients (flat spectrum)
			nbBands := 21
			lm := 3

			// Generate flat spectrum normalized coefficients
			numCoeffs := EBands[nbBands] << lm
			normL := make([]float64, numCoeffs)
			for i := range normL {
				normL[i] = 0.1 // Small uniform values
			}

			// Generate flat band energies
			bandLogE := make([]float64, nbBands*tc.channels)
			for i := range bandLogE {
				bandLogE[i] = 0.0 // Flat energy
			}

			var normR []float64
			if tc.channels == 2 {
				normR = make([]float64, numCoeffs)
				copy(normR, normL)
			}

			trim := AllocTrimAnalysis(
				normL,
				bandLogE,
				nbBands,
				lm,
				tc.channels,
				normR,
				nbBands, // intensity = nbBands (no intensity stereo)
				tc.tfEstimate,
				tc.equivRate,
				0, // surroundTrim
				0, // tonalitySlope
			)

			t.Logf("%s: equivRate=%d, tfEstimate=%.2f, trim=%d",
				tc.name, tc.equivRate, tc.tfEstimate, trim)

			if trim < tc.expectMinTrim || trim > tc.expectMaxTrim {
				t.Errorf("Trim %d out of expected range [%d, %d] for %s",
					trim, tc.expectMinTrim, tc.expectMaxTrim, tc.description)
			}
		})
	}
}

func TestComputeEquivRate(t *testing.T) {
	tests := []struct {
		name              string
		nbCompressedBytes int
		channels          int
		lm                int
		targetBitrate     int
		minExpect         int
		maxExpect         int
	}{
		{
			name:              "160 bytes mono 20ms",
			nbCompressedBytes: 160,
			channels:          1,
			lm:                3,
			targetBitrate:     0,
			minExpect:         60000,
			maxExpect:         70000,
		},
		{
			name:              "80 bytes mono 20ms",
			nbCompressedBytes: 80,
			channels:          1,
			lm:                3,
			targetBitrate:     0,
			minExpect:         28000,
			maxExpect:         36000,
		},
		{
			name:              "160 bytes stereo 20ms",
			nbCompressedBytes: 160,
			channels:          2,
			lm:                3,
			targetBitrate:     0,
			minExpect:         58000,
			maxExpect:         70000,
		},
		{
			name:              "40 bytes mono 10ms",
			nbCompressedBytes: 40,
			channels:          1,
			lm:                2,
			targetBitrate:     0,
			minExpect:         28000,
			maxExpect:         36000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			equivRate := ComputeEquivRate(tc.nbCompressedBytes, tc.channels, tc.lm, tc.targetBitrate)
			t.Logf("%s: equivRate=%d", tc.name, equivRate)

			if equivRate < tc.minExpect || equivRate > tc.maxExpect {
				t.Errorf("Equiv rate %d out of expected range [%d, %d]",
					equivRate, tc.minExpect, tc.maxExpect)
			}
		})
	}
}

func TestAllocTrimBitrateAdjustment(t *testing.T) {
	// Verify that allocation trim follows the bitrate rules from libopus:
	// - equiv_rate < 64000: trim = 4
	// - 64000 <= equiv_rate < 80000: trim interpolates 4 to 5
	// - equiv_rate >= 80000: trim = 5

	nbBands := 21
	lm := 3
	numCoeffs := EBands[nbBands] << lm
	normL := make([]float64, numCoeffs)
	bandLogE := make([]float64, nbBands)

	// Test at very low bitrate (should get trim ~4)
	trim32k := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0, 32000, 0, 0)
	trim64k := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0, 64000, 0, 0)
	trim72k := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0, 72000, 0, 0)
	trim80k := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0, 80000, 0, 0)
	trim128k := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0, 128000, 0, 0)

	t.Logf("Trim values by bitrate: 32k=%d, 64k=%d, 72k=%d, 80k=%d, 128k=%d",
		trim32k, trim64k, trim72k, trim80k, trim128k)

	// At 32k and 64k, should be at or near 4
	if trim32k > 5 {
		t.Errorf("At 32kbps, expected trim <= 5, got %d", trim32k)
	}

	// At 80k+, should be at or near 5
	if trim80k < 4 {
		t.Errorf("At 80kbps, expected trim >= 4, got %d", trim80k)
	}

	// trim72k should be between trim64k and trim80k
	if trim72k < trim64k-1 || trim72k > trim80k+1 {
		t.Errorf("At 72kbps, expected trim between %d and %d, got %d", trim64k, trim80k, trim72k)
	}
}

func TestAllocTrimTFEstimate(t *testing.T) {
	// Test that TF estimate properly reduces trim for transient signals
	nbBands := 21
	lm := 3
	numCoeffs := EBands[nbBands] << lm
	normL := make([]float64, numCoeffs)
	bandLogE := make([]float64, nbBands)
	equivRate := 80000 // Use neutral bitrate

	trimNoTransient := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.0, equivRate, 0, 0)
	trimLowTransient := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.3, equivRate, 0, 0)
	trimHighTransient := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.8, equivRate, 0, 0)

	t.Logf("Trim values by TF estimate: 0.0=%d, 0.3=%d, 0.8=%d",
		trimNoTransient, trimLowTransient, trimHighTransient)

	// Higher TF estimate should give lower trim
	if trimLowTransient > trimNoTransient {
		t.Errorf("TF 0.3 trim (%d) should be <= TF 0.0 trim (%d)", trimLowTransient, trimNoTransient)
	}
	if trimHighTransient > trimLowTransient {
		t.Errorf("TF 0.8 trim (%d) should be <= TF 0.3 trim (%d)", trimHighTransient, trimLowTransient)
	}
}

func TestAllocTrimSurroundTrimAdjustment(t *testing.T) {
	nbBands := 21
	lm := 3
	numCoeffs := EBands[nbBands] << lm
	normL := make([]float64, numCoeffs)
	bandLogE := make([]float64, nbBands)
	equivRate := 80000

	base := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.0, equivRate, 0.0, 0.0)
	plus := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.0, equivRate, 1.0, 0.0)
	minus := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.0, equivRate, -1.0, 0.0)

	if plus > base {
		t.Fatalf("positive surroundTrim should not increase trim: base=%d plus=%d", base, plus)
	}
	if minus < base {
		t.Fatalf("negative surroundTrim should not decrease trim: base=%d minus=%d", base, minus)
	}
}
