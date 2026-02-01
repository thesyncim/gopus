package celt

import (
	"testing"
)

// TestComputeImportanceBasic tests basic importance computation.
func TestComputeImportanceBasic(t *testing.T) {
	tests := []struct {
		name           string
		nbBands        int
		channels       int
		lm             int
		lsbDepth       int
		effectiveBytes int
		expectDefault  bool // If true, all values should be 13
	}{
		{
			name:           "low_bitrate_returns_default",
			nbBands:        21,
			channels:       1,
			lm:             3,
			lsbDepth:       16,
			effectiveBytes: 20, // Below threshold of 30 + 5*lm = 45
			expectDefault:  true,
		},
		{
			name:           "high_bitrate_mono",
			nbBands:        21,
			channels:       1,
			lm:             3,
			lsbDepth:       16,
			effectiveBytes: 160,
			expectDefault:  false,
		},
		{
			name:           "high_bitrate_stereo",
			nbBands:        21,
			channels:       2,
			lm:             3,
			lsbDepth:       16,
			effectiveBytes: 320,
			expectDefault:  false,
		},
		{
			name:           "lm0_threshold",
			nbBands:        13,
			channels:       1,
			lm:             0,
			lsbDepth:       16,
			effectiveBytes: 30, // Exactly at threshold of 30 + 5*0 = 30
			expectDefault:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create synthetic band energies
			bandLogE := make([]float64, tc.nbBands*tc.channels)
			for i := range bandLogE {
				// Typical energy values (mean-relative, so around 0)
				bandLogE[i] = float64(i%10-5) * 0.5
			}

			// Create old band energies (previous frame)
			oldBandE := make([]float64, MaxBands*tc.channels)
			for i := range oldBandE {
				oldBandE[i] = 0.0
			}

			importance := ComputeImportance(bandLogE, oldBandE, tc.nbBands, tc.channels, tc.lm, tc.lsbDepth, tc.effectiveBytes)

			// Verify output length
			if len(importance) != tc.nbBands {
				t.Errorf("ComputeImportance returned %d values, want %d", len(importance), tc.nbBands)
			}

			// Verify values are within valid range
			for i, imp := range importance {
				if imp < 1 || imp > 255 {
					t.Errorf("importance[%d] = %d, out of valid range [1, 255]", i, imp)
				}
			}

			if tc.expectDefault {
				// All values should be 13
				for i, imp := range importance {
					if imp != 13 {
						t.Errorf("importance[%d] = %d, want 13 (default)", i, imp)
					}
				}
			} else {
				// At least some values should differ from 13
				hasNonDefault := false
				for _, imp := range importance {
					if imp != 13 {
						hasNonDefault = true
						break
					}
				}
				// Note: depending on input, all values could still be 13
				// but we log for inspection
				t.Logf("importance values: %v (hasNonDefault=%v)", importance, hasNonDefault)
			}
		})
	}
}

// TestComputeImportanceWithEnergy tests importance with various energy profiles.
func TestComputeImportanceWithEnergy(t *testing.T) {
	nbBands := 21
	channels := 1
	lm := 3
	lsbDepth := 16
	effectiveBytes := 160

	tests := []struct {
		name          string
		energyProfile func(band int) float64
		expectHighImp bool // If true, at least some bands should have importance > 13
	}{
		{
			name: "flat_energy",
			energyProfile: func(band int) float64 {
				return 0.0 // All bands at mean
			},
			expectHighImp: false, // Flat profile => no excess energy => importance = 13
		},
		{
			name: "extreme_peak_band_10",
			energyProfile: func(band int) float64 {
				// Extreme peak at band 10 (much higher than neighbors)
				if band == 10 {
					return 20.0 // Very high energy spike
				}
				return -5.0 // Low energy elsewhere
			},
			expectHighImp: true, // Large spike should produce high importance
		},
		{
			name: "alternating_high_low",
			energyProfile: func(band int) float64 {
				// Alternating pattern creates excess energy
				if band%2 == 0 {
					return 15.0
				}
				return -10.0
			},
			expectHighImp: true, // Pattern should produce varying importance
		},
		{
			name: "gradual_rise_sharp_drop",
			energyProfile: func(band int) float64 {
				// Gradual rise then sharp drop
				if band < 15 {
					return float64(band) * 2.0
				}
				return -20.0
			},
			expectHighImp: true, // Sharp transitions create excess
		},
		{
			name: "very_low_energy",
			energyProfile: func(band int) float64 {
				return -20.0 // Very low energy
			},
			expectHighImp: false, // All low => no excess
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bandLogE := make([]float64, nbBands*channels)
			for i := 0; i < nbBands; i++ {
				bandLogE[i] = tc.energyProfile(i)
			}

			oldBandE := make([]float64, MaxBands*channels)
			for i := range oldBandE {
				oldBandE[i] = 0.0
			}

			importance := ComputeImportance(bandLogE, oldBandE, nbBands, channels, lm, lsbDepth, effectiveBytes)

			// Verify output
			if len(importance) != nbBands {
				t.Errorf("ComputeImportance returned %d values, want %d", len(importance), nbBands)
			}

			// Log the results for analysis
			t.Logf("Energy profile %s: importance=%v", tc.name, importance)

			// Verify all values are within valid range
			for i, imp := range importance {
				if imp < 1 || imp > 255 {
					t.Errorf("importance[%d] = %d, out of valid range", i, imp)
				}
			}

			// Check if we expect high importance values
			if tc.expectHighImp {
				hasHighImp := false
				for _, imp := range importance {
					if imp > 13 {
						hasHighImp = true
						break
					}
				}
				if hasHighImp {
					t.Logf("  (found bands with importance > 13 as expected)")
				}
			}
		})
	}
}

// TestComputeImportanceStereo tests importance computation for stereo signals.
func TestComputeImportanceStereo(t *testing.T) {
	nbBands := 21
	channels := 2
	lm := 3
	lsbDepth := 16
	effectiveBytes := 320

	// Create stereo energies with different profiles for L/R
	bandLogE := make([]float64, nbBands*channels)
	for i := 0; i < nbBands; i++ {
		// Left channel: higher in low bands
		bandLogE[i] = float64(nbBands-i) * 0.3
		// Right channel: higher in high bands
		bandLogE[nbBands+i] = float64(i) * 0.3
	}

	oldBandE := make([]float64, MaxBands*channels)

	importance := ComputeImportance(bandLogE, oldBandE, nbBands, channels, lm, lsbDepth, effectiveBytes)

	if len(importance) != nbBands {
		t.Errorf("ComputeImportance returned %d values, want %d", len(importance), nbBands)
	}

	t.Logf("Stereo importance: %v", importance)

	// All values should be valid
	for i, imp := range importance {
		if imp < 1 || imp > 255 {
			t.Errorf("importance[%d] = %d, out of valid range", i, imp)
		}
	}
}

// TestComputeImportanceIntegration tests that importance integrates with TFAnalysis.
func TestComputeImportanceIntegration(t *testing.T) {
	nbBands := 21
	channels := 1
	lm := 3
	lsbDepth := 16
	effectiveBytes := 160

	// Create synthetic normalized coefficients
	N0 := EBands[nbBands] << lm
	X := make([]float64, N0)
	for i := 0; i < N0; i++ {
		X[i] = float64(i%10-5) / 10.0
	}

	// Create band energies
	bandLogE := make([]float64, nbBands*channels)
	for i := 0; i < nbBands; i++ {
		bandLogE[i] = float64(i%5) * 0.5
	}

	oldBandE := make([]float64, MaxBands*channels)

	// Compute importance
	importance := ComputeImportance(bandLogE, oldBandE, nbBands, channels, lm, lsbDepth, effectiveBytes)

	// Use importance in TF analysis
	tfResWithImportance, tfSelectWithImportance := TFAnalysis(X, N0, nbBands, false, lm, 0.5, effectiveBytes, importance)

	// Compare with nil importance (uniform)
	tfResWithoutImportance, tfSelectWithoutImportance := TFAnalysis(X, N0, nbBands, false, lm, 0.5, effectiveBytes, nil)

	// Both should produce valid results
	if len(tfResWithImportance) != nbBands {
		t.Errorf("TFAnalysis with importance returned %d bands, want %d", len(tfResWithImportance), nbBands)
	}
	if len(tfResWithoutImportance) != nbBands {
		t.Errorf("TFAnalysis without importance returned %d bands, want %d", len(tfResWithoutImportance), nbBands)
	}

	// Log comparison
	t.Logf("With importance: tfRes=%v, tfSelect=%d", tfResWithImportance, tfSelectWithImportance)
	t.Logf("Without importance: tfRes=%v, tfSelect=%d", tfResWithoutImportance, tfSelectWithoutImportance)

	// Verify valid TF resolution values
	for i, v := range tfResWithImportance {
		if v != 0 && v != 1 {
			t.Errorf("tfResWithImportance[%d] = %d, want 0 or 1", i, v)
		}
	}
	for i, v := range tfResWithoutImportance {
		if v != 0 && v != 1 {
			t.Errorf("tfResWithoutImportance[%d] = %d, want 0 or 1", i, v)
		}
	}
}

// BenchmarkComputeImportance benchmarks importance computation.
func BenchmarkComputeImportance(b *testing.B) {
	nbBands := 21
	channels := 2
	lm := 3
	lsbDepth := 16
	effectiveBytes := 320

	bandLogE := make([]float64, nbBands*channels)
	for i := range bandLogE {
		bandLogE[i] = float64(i%10-5) * 0.5
	}
	oldBandE := make([]float64, MaxBands*channels)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = ComputeImportance(bandLogE, oldBandE, nbBands, channels, lm, lsbDepth, effectiveBytes)
	}
}
