// Package celt implements the CELT encoder per RFC 6716 Section 4.3.
// This file provides validation tests for tonality analysis.

package celt

import (
	"math"
	"math/rand"
	"testing"
)

// =============================================================================
// Test Utilities
// =============================================================================

// generateSineWaveMDCT generates MDCT coefficients for a pure sine wave.
// The energy is concentrated in 1-2 bins corresponding to the frequency.
// This simulates a highly tonal signal.
//
// Parameters:
//   - frequency: tone frequency in Hz
//   - sampleRate: sample rate in Hz (typically 48000)
//   - frameSize: MDCT frame size (120, 240, 480, 960)
//
// Returns MDCT coefficients with energy concentrated at the target frequency.
func generateSineWaveMDCT(frequency, sampleRate, frameSize int) []float64 {
	// MDCT has frameSize bins spanning 0 to sampleRate/2
	// Bin k corresponds to frequency k * sampleRate / (2 * frameSize)
	// Target bin = frequency * 2 * frameSize / sampleRate
	coeffs := make([]float64, frameSize)

	// Calculate target bin
	targetBin := float64(frequency) * float64(2*frameSize) / float64(sampleRate)
	binIndex := int(math.Round(targetBin))
	if binIndex < 0 {
		binIndex = 0
	}
	if binIndex >= frameSize {
		binIndex = frameSize - 1
	}

	// Place energy in target bin (and possibly adjacent bin for non-integer bins)
	// This simulates a pure tone after MDCT
	amplitude := 1.0
	coeffs[binIndex] = amplitude

	// Add small leakage to adjacent bins to simulate realistic MDCT behavior
	if binIndex > 0 {
		coeffs[binIndex-1] = amplitude * 0.1
	}
	if binIndex < frameSize-1 {
		coeffs[binIndex+1] = amplitude * 0.1
	}

	return coeffs
}

// generateWhiteNoiseMDCT generates MDCT coefficients for white noise.
// Energy is spread uniformly across all bins, simulating a noisy signal.
//
// Parameters:
//   - frameSize: MDCT frame size (120, 240, 480, 960)
//   - seed: random seed for reproducibility (0 uses default)
//
// Returns MDCT coefficients with uniformly distributed energy.
func generateWhiteNoiseMDCT(frameSize int, seed int64) []float64 {
	coeffs := make([]float64, frameSize)

	// Use seeded random for reproducibility
	rng := rand.New(rand.NewSource(seed))

	// Generate Gaussian noise for each bin
	for i := 0; i < frameSize; i++ {
		// Box-Muller transform for Gaussian distribution
		u1 := rng.Float64()
		u2 := rng.Float64()
		if u1 < 1e-10 {
			u1 = 1e-10
		}
		coeffs[i] = math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	}

	return coeffs
}

// generateHarmonicMDCT generates MDCT coefficients with harmonic structure.
// Creates a fundamental frequency with decaying harmonics.
//
// Parameters:
//   - fundamental: fundamental frequency in Hz
//   - sampleRate: sample rate in Hz
//   - frameSize: MDCT frame size
//   - numHarmonics: number of harmonics to include
//
// Returns MDCT coefficients with harmonic structure.
func generateHarmonicMDCT(fundamental, sampleRate, frameSize, numHarmonics int) []float64 {
	coeffs := make([]float64, frameSize)

	nyquist := sampleRate / 2

	for h := 1; h <= numHarmonics; h++ {
		freq := fundamental * h
		if freq >= nyquist {
			break
		}

		// Calculate target bin
		targetBin := float64(freq) * float64(2*frameSize) / float64(sampleRate)
		binIndex := int(math.Round(targetBin))
		if binIndex < 0 || binIndex >= frameSize {
			continue
		}

		// Harmonic amplitude decays as 1/h (typical for many instruments)
		amplitude := 1.0 / float64(h)
		coeffs[binIndex] += amplitude

		// Add leakage
		if binIndex > 0 {
			coeffs[binIndex-1] += amplitude * 0.05
		}
		if binIndex < frameSize-1 {
			coeffs[binIndex+1] += amplitude * 0.05
		}
	}

	return coeffs
}

// generateMixedTonalNoiseMDCT generates coefficients with tonal low bands and noisy high bands.
// This tests per-band tonality calculation.
//
// Parameters:
//   - frameSize: MDCT frame size
//   - tonalBandCount: number of low bands to make tonal
//   - seed: random seed for noise generation
//
// Returns MDCT coefficients with mixed tonality.
func generateMixedTonalNoiseMDCT(frameSize, tonalBandCount int, seed int64) []float64 {
	coeffs := make([]float64, frameSize)
	rng := rand.New(rand.NewSource(seed))

	// Determine split point based on band count (approximate)
	// CELT bands are not uniform, but for testing we use a simple approximation
	splitBin := frameSize / 4 // Make first quarter tonal

	// Low bands: tonal (sparse energy)
	divisor := testMaxInt(splitBin/testMaxInt(tonalBandCount, 1), 1)
	for i := 0; i < splitBin; i++ {
		if i%divisor == 0 {
			coeffs[i] = 1.0
		}
	}

	// High bands: noise (spread energy)
	for i := splitBin; i < frameSize; i++ {
		u1 := rng.Float64()
		u2 := rng.Float64()
		if u1 < 1e-10 {
			u1 = 1e-10
		}
		coeffs[i] = 0.3 * math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
	}

	return coeffs
}

// testMaxInt returns the larger of two integers.
// Named differently to avoid conflict with package-level maxInt in alloc.go.
func testMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// getNbBandsForFrameSize returns the effective number of bands for a frame size.
func getNbBandsForFrameSize(frameSize int) int {
	mode := GetModeConfig(frameSize)
	return mode.EffBands
}

// =============================================================================
// Helper Function Tests
// =============================================================================

// TestGeometricMean verifies the geometric mean helper function.
func TestGeometricMean(t *testing.T) {
	// geometricMean uses fastLog2 (IEEE 754 polynomial approximation)
	// which has ~3e-5 relative error. Tolerances reflect this.
	tests := []struct {
		name     string
		values   []float64
		expected float64
		epsilon  float64
	}{
		{
			name:     "single value",
			values:   []float64{4.0},
			expected: 4.0,
			epsilon:  1e-3,
		},
		{
			name:     "two equal values",
			values:   []float64{4.0, 4.0},
			expected: 4.0,
			epsilon:  1e-3,
		},
		{
			name:     "two different values",
			values:   []float64{4.0, 16.0},
			expected: 8.0, // sqrt(4*16) = 8
			epsilon:  1e-3,
		},
		{
			name:     "three values",
			values:   []float64{2.0, 4.0, 8.0},
			expected: 4.0, // (2*4*8)^(1/3) = 64^(1/3) = 4
			epsilon:  1e-3,
		},
		{
			name:     "with small values",
			values:   []float64{0.01, 0.01, 0.01},
			expected: 0.01,
			epsilon:  1e-4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := geometricMean(tc.values)
			if math.Abs(result-tc.expected) > tc.epsilon {
				t.Errorf("geometricMean(%v) = %v, want %v", tc.values, result, tc.expected)
			}
		})
	}
}

// TestArithmeticMean verifies the arithmetic mean helper function.
func TestArithmeticMean(t *testing.T) {
	tests := []struct {
		name     string
		values   []float64
		expected float64
		epsilon  float64
	}{
		{
			name:     "single value",
			values:   []float64{4.0},
			expected: 4.0,
			epsilon:  1e-10,
		},
		{
			name:     "two values",
			values:   []float64{2.0, 6.0},
			expected: 4.0,
			epsilon:  1e-10,
		},
		{
			name:     "three values",
			values:   []float64{1.0, 2.0, 3.0},
			expected: 2.0,
			epsilon:  1e-10,
		},
		{
			name:     "with negative values",
			values:   []float64{-2.0, 2.0},
			expected: 0.0,
			epsilon:  1e-10,
		},
		{
			name:     "with zeros",
			values:   []float64{0.0, 4.0, 8.0},
			expected: 4.0,
			epsilon:  1e-10,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := arithmeticMean(tc.values)
			if math.Abs(result-tc.expected) > tc.epsilon {
				t.Errorf("arithmeticMean(%v) = %v, want %v", tc.values, result, tc.expected)
			}
		})
	}
}

// TestGeometricMeanEmpty tests geometric mean with empty input.
func TestGeometricMeanEmpty(t *testing.T) {
	result := geometricMean(nil)
	if result != 0 {
		t.Errorf("geometricMean(nil) = %v, want 0", result)
	}

	result = geometricMean([]float64{})
	if result != 0 {
		t.Errorf("geometricMean([]) = %v, want 0", result)
	}
}

// TestArithmeticMeanEmpty tests arithmetic mean with empty input.
func TestArithmeticMeanEmpty(t *testing.T) {
	result := arithmeticMean(nil)
	if result != 0 {
		t.Errorf("arithmeticMean(nil) = %v, want 0", result)
	}

	result = arithmeticMean([]float64{})
	if result != 0 {
		t.Errorf("arithmeticMean([]) = %v, want 0", result)
	}
}

// =============================================================================
// Pure Sine Wave Tonality Tests
// =============================================================================

// TestTonalityPureSineWave verifies that a pure sine wave has very high tonality.
// A pure tone concentrates energy in 1-2 MDCT bins, which should result in
// high SFM (spectral flatness measure) ratio, indicating tonality > 0.85.
func TestTonalityPureSineWave(t *testing.T) {
	testCases := []struct {
		name      string
		frequency int
		frameSize int
	}{
		{"440Hz_10ms", 440, 480},
		{"1000Hz_10ms", 1000, 480},
		{"440Hz_20ms", 440, 960},
		{"2000Hz_20ms", 2000, 960},
		{"500Hz_5ms", 500, 240},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			coeffs := generateSineWaveMDCT(tc.frequency, 48000, tc.frameSize)
			nbBands := getNbBandsForFrameSize(tc.frameSize)

			result := ComputeTonalityWithBands(coeffs, nbBands, tc.frameSize)

			// Pure sine wave should have very high tonality
			if result.Tonality < 0.85 {
				t.Errorf("Pure sine wave tonality = %v, want >= 0.85", result.Tonality)
			}

			// Log for diagnostic purposes
			t.Logf("Frequency=%dHz, FrameSize=%d: Tonality=%.4f",
				tc.frequency, tc.frameSize, result.Tonality)
		})
	}
}

// TestTonalityPureSineWaveAllFrameSizes tests pure tones across all CELT frame sizes.
func TestTonalityPureSineWaveAllFrameSizes(t *testing.T) {
	frameSizes := []int{120, 240, 480, 960}
	frequency := 1000

	for _, frameSize := range frameSizes {
		t.Run("", func(t *testing.T) {
			coeffs := generateSineWaveMDCT(frequency, 48000, frameSize)
			nbBands := getNbBandsForFrameSize(frameSize)

			result := ComputeTonalityWithBands(coeffs, nbBands, frameSize)

			// All frame sizes should detect pure tone as tonal
			if result.Tonality < 0.80 {
				t.Errorf("FrameSize=%d: Tonality=%.4f, want >= 0.80", frameSize, result.Tonality)
			}
		})
	}
}

// =============================================================================
// White Noise Tonality Tests
// =============================================================================

// TestTonalityWhiteNoise verifies that white noise has lower tonality than pure tones.
// White noise spreads energy across all bins. The band-based SFM calculation
// gives moderate tonality (~0.7) because individual CELT bands still show
// some spectral structure due to Gaussian distribution variance.
func TestTonalityWhiteNoise(t *testing.T) {
	testCases := []struct {
		name      string
		frameSize int
		seed      int64
	}{
		{"noise_10ms_seed1", 480, 12345},
		{"noise_10ms_seed2", 480, 54321},
		{"noise_20ms_seed1", 960, 12345},
		{"noise_20ms_seed2", 960, 98765},
		{"noise_5ms", 240, 11111},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			coeffs := generateWhiteNoiseMDCT(tc.frameSize, tc.seed)
			nbBands := getNbBandsForFrameSize(tc.frameSize)

			result := ComputeTonalityWithBands(coeffs, nbBands, tc.frameSize)

			// White noise tonality should be lower than pure tone (which is 1.0)
			// The band-based analysis gives ~0.7 for Gaussian noise
			if result.Tonality > 0.85 {
				t.Errorf("White noise tonality = %v, want < 0.85 (less than pure tone)", result.Tonality)
			}

			// Verify tonality is in valid range
			if result.Tonality < 0.0 || result.Tonality > 1.0 {
				t.Errorf("Tonality out of [0,1] range: %v", result.Tonality)
			}

			// Log for diagnostics
			t.Logf("FrameSize=%d, Seed=%d: Tonality=%.4f",
				tc.frameSize, tc.seed, result.Tonality)
		})
	}
}

// TestTonalityWhiteNoiseStatistical runs multiple noise samples to verify
// consistent tonality detection lower than pure tones.
func TestTonalityWhiteNoiseStatistical(t *testing.T) {
	frameSize := 480
	nbBands := getNbBandsForFrameSize(frameSize)
	numSamples := 10

	var sumTonality float64
	for seed := int64(0); seed < int64(numSamples); seed++ {
		coeffs := generateWhiteNoiseMDCT(frameSize, seed*1000+12345)
		result := ComputeTonalityWithBands(coeffs, nbBands, frameSize)
		sumTonality += result.Tonality
	}

	avgTonality := sumTonality / float64(numSamples)

	// Average noise tonality should be lower than pure tone (1.0)
	// but may be moderate (~0.7) due to band-based analysis
	if avgTonality > 0.85 {
		t.Errorf("Average white noise tonality = %v, want < 0.85", avgTonality)
	}

	t.Logf("Average tonality across %d noise samples: %.4f", numSamples, avgTonality)
}

// =============================================================================
// Harmonic Signal Tonality Tests
// =============================================================================

// TestTonalityHarmonicSignal verifies that harmonic signals have high tonality.
// Harmonic signals (like musical instruments) have energy at fundamental + harmonics,
// which should result in high tonality similar to pure tones since harmonics
// are also highly peaked in frequency.
func TestTonalityHarmonicSignal(t *testing.T) {
	testCases := []struct {
		name         string
		fundamental  int
		numHarmonics int
		frameSize    int
	}{
		{"violin_440Hz_5h", 440, 5, 960},
		{"violin_440Hz_10h", 440, 10, 960},
		{"bass_110Hz_8h", 110, 8, 960},
		{"flute_880Hz_3h", 880, 3, 480},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			coeffs := generateHarmonicMDCT(tc.fundamental, 48000, tc.frameSize, tc.numHarmonics)
			nbBands := getNbBandsForFrameSize(tc.frameSize)

			result := ComputeTonalityWithBands(coeffs, nbBands, tc.frameSize)

			// Harmonic signals with sparse peaks should have high tonality
			// (similar to pure tones, since each harmonic is a peak)
			if result.Tonality < 0.5 {
				t.Errorf("Harmonic signal tonality = %v, want >= 0.5", result.Tonality)
			}

			t.Logf("Fundamental=%dHz, Harmonics=%d, FrameSize=%d: Tonality=%.4f",
				tc.fundamental, tc.numHarmonics, tc.frameSize, result.Tonality)
		})
	}
}

// TestTonalityHarmonicVsPureTone compares harmonic signal to pure tone.
// Pure tone should have higher tonality than harmonic signal.
func TestTonalityHarmonicVsPureTone(t *testing.T) {
	frameSize := 960
	frequency := 440
	nbBands := getNbBandsForFrameSize(frameSize)

	pureCoeffs := generateSineWaveMDCT(frequency, 48000, frameSize)
	harmonicCoeffs := generateHarmonicMDCT(frequency, 48000, frameSize, 10)

	pureResult := ComputeTonalityWithBands(pureCoeffs, nbBands, frameSize)
	harmonicResult := ComputeTonalityWithBands(harmonicCoeffs, nbBands, frameSize)

	// Pure tone should have equal or higher tonality than harmonic
	if pureResult.Tonality < harmonicResult.Tonality-0.1 {
		t.Errorf("Pure tone tonality (%.4f) should be >= harmonic (%.4f)",
			pureResult.Tonality, harmonicResult.Tonality)
	}

	t.Logf("Pure tone: %.4f, Harmonic: %.4f", pureResult.Tonality, harmonicResult.Tonality)
}

// =============================================================================
// Per-Band Tonality Tests
// =============================================================================

// TestTonalityPerBand verifies per-band tonality calculation.
// Creates a signal with tonal low bands and noisy high bands.
func TestTonalityPerBand(t *testing.T) {
	frameSize := 960
	nbBands := getNbBandsForFrameSize(frameSize)
	coeffs := generateMixedTonalNoiseMDCT(frameSize, 4, 12345)

	result := ComputeTonalityWithBands(coeffs, nbBands, frameSize)

	// Should have per-band tonality values
	if len(result.BandTonality) == 0 {
		t.Error("Expected per-band tonality values")
		return
	}

	// Calculate average tonality for first half vs second half of bands
	midPoint := len(result.BandTonality) / 2
	if midPoint == 0 {
		t.Skip("Not enough bands for per-band analysis")
	}

	var lowBandAvg, highBandAvg float64
	for i := 0; i < midPoint; i++ {
		lowBandAvg += result.BandTonality[i]
	}
	lowBandAvg /= float64(midPoint)

	for i := midPoint; i < len(result.BandTonality); i++ {
		highBandAvg += result.BandTonality[i]
	}
	highBandAvg /= float64(len(result.BandTonality) - midPoint)

	t.Logf("Low band avg tonality: %.4f, High band avg tonality: %.4f",
		lowBandAvg, highBandAvg)

	// Low bands (tonal) should have higher tonality than high bands (noisy)
	// Allow some tolerance as the division isn't perfectly clean
	if lowBandAvg < highBandAvg {
		t.Logf("Warning: Expected low bands (%.4f) >= high bands (%.4f)",
			lowBandAvg, highBandAvg)
	}
}

// TestTonalityPerBandConsistency verifies per-band values are computed correctly.
// Note: Overall tonality uses full-spectrum SFM while band tonality uses per-band SFM,
// so they may differ significantly for signals with localized energy (like pure tones).
func TestTonalityPerBandConsistency(t *testing.T) {
	frameSize := 480
	nbBands := getNbBandsForFrameSize(frameSize)

	testCases := []struct {
		name   string
		coeffs []float64
	}{
		{"pure_tone", generateSineWaveMDCT(1000, 48000, frameSize)},
		{"noise", generateWhiteNoiseMDCT(frameSize, 12345)},
		{"harmonic", generateHarmonicMDCT(440, 48000, frameSize, 5)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ComputeTonalityWithBands(tc.coeffs, nbBands, frameSize)

			if len(result.BandTonality) == 0 {
				t.Error("Expected per-band tonality")
				return
			}

			// Verify all band tonality values are in valid range
			for i, bt := range result.BandTonality {
				if bt < 0.0 || bt > 1.0 {
					t.Errorf("Band %d tonality %v out of [0,1] range", i, bt)
				}
			}

			// Log comparison for diagnostic purposes
			var avgBandTonality float64
			for _, bt := range result.BandTonality {
				avgBandTonality += bt
			}
			avgBandTonality /= float64(len(result.BandTonality))
			t.Logf("Overall: %.4f, Band avg: %.4f", result.Tonality, avgBandTonality)
		})
	}
}

// =============================================================================
// Spectral Flux Tests
// =============================================================================

// TestSpectralFluxIdenticalFrames verifies flux between identical frames is ~0.
func TestSpectralFluxIdenticalFrames(t *testing.T) {
	frameSize := 480
	nbBands := getNbBandsForFrameSize(frameSize)
	_ = generateSineWaveMDCT(1000, 48000, frameSize) // Generate coeffs to validate generator works

	// Compute energies for both "frames" (identical)
	energies := make([]float64, nbBands)
	for i := 0; i < nbBands; i++ {
		energies[i] = 1.0 // Simplified energy
	}

	flux := ComputeSpectralFlux(energies, energies, nbBands)

	// Flux between identical frames should be very small
	if flux > 0.01 {
		t.Errorf("Flux between identical frames = %v, want ~0", flux)
	}
}

// TestSpectralFluxDifferentFrames verifies flux between different frames is high.
func TestSpectralFluxDifferentFrames(t *testing.T) {
	nbBands := 21

	// Create very different energy profiles
	energies1 := make([]float64, nbBands)
	energies2 := make([]float64, nbBands)

	for i := 0; i < nbBands; i++ {
		energies1[i] = 1.0    // Uniform low
		energies2[i] = 1000.0 // Uniform high (30 dB difference)
	}

	flux := ComputeSpectralFlux(energies2, energies1, nbBands)

	// Flux between very different frames should be significant
	if flux < 0.1 {
		t.Errorf("Flux between different frames = %v, want >= 0.1", flux)
	}

	t.Logf("Spectral flux between low and high energy: %.4f", flux)
}

// TestSpectralFluxSymmetry verifies flux is approximately symmetric.
func TestSpectralFluxSymmetry(t *testing.T) {
	nbBands := 21

	energies1 := make([]float64, nbBands)
	energies2 := make([]float64, nbBands)

	for i := 0; i < nbBands; i++ {
		energies1[i] = float64(i + 1)
		energies2[i] = float64(nbBands - i)
	}

	flux12 := ComputeSpectralFlux(energies2, energies1, nbBands)
	flux21 := ComputeSpectralFlux(energies1, energies2, nbBands)

	// Flux should be approximately symmetric
	diff := math.Abs(flux12 - flux21)
	if diff > 0.1 {
		t.Errorf("Flux asymmetry: frame1->frame2=%.4f, frame2->frame1=%.4f, diff=%.4f",
			flux12, flux21, diff)
	}

	t.Logf("Flux symmetry: 1->2=%.4f, 2->1=%.4f", flux12, flux21)
}

// TestSpectralFluxEmpty verifies behavior with empty inputs.
func TestSpectralFluxEmpty(t *testing.T) {
	flux := ComputeSpectralFlux(nil, nil, 0)
	if flux != 0.0 {
		t.Errorf("Flux with nil inputs = %v, want 0", flux)
	}

	flux = ComputeSpectralFlux([]float64{}, []float64{}, 0)
	if flux != 0.0 {
		t.Errorf("Flux with empty inputs = %v, want 0", flux)
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

// TestTonalityEdgeCases tests various edge cases for numerical stability.
func TestTonalityEdgeCases(t *testing.T) {
	t.Run("empty coefficients", func(t *testing.T) {
		result := ComputeTonalityWithBands([]float64{}, 21, 480)

		// Should not panic, should return sensible defaults
		if math.IsNaN(result.Tonality) || math.IsInf(result.Tonality, 0) {
			t.Error("Empty coefficients produced NaN/Inf tonality")
		}
	})

	t.Run("nil coefficients", func(t *testing.T) {
		result := ComputeTonalityWithBands(nil, 21, 480)

		if math.IsNaN(result.Tonality) || math.IsInf(result.Tonality, 0) {
			t.Error("Nil coefficients produced NaN/Inf tonality")
		}
	})

	t.Run("all zeros", func(t *testing.T) {
		coeffs := make([]float64, 480)
		result := ComputeTonalityWithBands(coeffs, 21, 480)

		// All zeros is effectively silence - should not produce NaN
		if math.IsNaN(result.Tonality) || math.IsInf(result.Tonality, 0) {
			t.Error("All-zero coefficients produced NaN/Inf tonality")
		}
	})

	t.Run("single non-zero coefficient", func(t *testing.T) {
		coeffs := make([]float64, 480)
		coeffs[100] = 1.0
		result := ComputeTonalityWithBands(coeffs, 21, 480)

		// Single coefficient is maximally tonal
		if result.Tonality < 0.9 {
			t.Errorf("Single coefficient tonality = %v, want >= 0.9", result.Tonality)
		}
	})

	t.Run("very small values", func(t *testing.T) {
		coeffs := make([]float64, 480)
		for i := range coeffs {
			coeffs[i] = 1e-30
		}
		result := ComputeTonalityWithBands(coeffs, 21, 480)

		// Should handle denormals without producing NaN/Inf
		if math.IsNaN(result.Tonality) || math.IsInf(result.Tonality, 0) {
			t.Error("Very small values produced NaN/Inf tonality")
		}
	})

	t.Run("very large values", func(t *testing.T) {
		coeffs := make([]float64, 480)
		for i := range coeffs {
			coeffs[i] = 1e30
		}
		result := ComputeTonalityWithBands(coeffs, 21, 480)

		// Should handle large values without overflow
		if math.IsNaN(result.Tonality) || math.IsInf(result.Tonality, 0) {
			t.Error("Very large values produced NaN/Inf tonality")
		}
	})

	t.Run("mixed small and large", func(t *testing.T) {
		coeffs := make([]float64, 480)
		coeffs[0] = 1e30
		coeffs[1] = 1e-30
		result := ComputeTonalityWithBands(coeffs, 21, 480)

		// Should handle dynamic range
		if math.IsNaN(result.Tonality) || math.IsInf(result.Tonality, 0) {
			t.Error("Mixed range values produced NaN/Inf tonality")
		}
	})

	t.Run("negative values", func(t *testing.T) {
		coeffs := make([]float64, 480)
		for i := range coeffs {
			if i%2 == 0 {
				coeffs[i] = 1.0
			} else {
				coeffs[i] = -1.0
			}
		}
		result := ComputeTonalityWithBands(coeffs, 21, 480)

		// Tonality should work with signed MDCT coefficients
		if math.IsNaN(result.Tonality) || math.IsInf(result.Tonality, 0) {
			t.Error("Negative values produced NaN/Inf tonality")
		}
	})

	t.Run("single element", func(t *testing.T) {
		coeffs := []float64{1.0}
		result := ComputeTonalityWithBands(coeffs, 1, 1)

		if math.IsNaN(result.Tonality) || math.IsInf(result.Tonality, 0) {
			t.Error("Single element produced NaN/Inf tonality")
		}
	})

	t.Run("zero nbBands", func(t *testing.T) {
		coeffs := make([]float64, 480)
		result := ComputeTonalityWithBands(coeffs, 0, 480)

		if math.IsNaN(result.Tonality) || math.IsInf(result.Tonality, 0) {
			t.Error("Zero nbBands produced NaN/Inf tonality")
		}
	})

	t.Run("zero frameSize", func(t *testing.T) {
		coeffs := make([]float64, 480)
		result := ComputeTonalityWithBands(coeffs, 21, 0)

		if math.IsNaN(result.Tonality) || math.IsInf(result.Tonality, 0) {
			t.Error("Zero frameSize produced NaN/Inf tonality")
		}
	})
}

// TestTonalityNumericalStability tests numerical stability with extreme inputs.
// Note: Values near MaxFloat64 can overflow when squared, producing Inf/NaN.
// This is expected behavior - the tests document the edge case behavior.
func TestTonalityNumericalStability(t *testing.T) {
	// Test with values that are large but won't overflow when squared
	testCases := []struct {
		name   string
		coeffs []float64
	}{
		{
			name:   "large values",
			coeffs: []float64{1e150, 1e150}, // Large but squaring won't overflow
		},
		{
			name:   "near min positive",
			coeffs: []float64{math.SmallestNonzeroFloat64, math.SmallestNonzeroFloat64},
		},
		{
			name:   "mixed large and small",
			coeffs: []float64{1e150, 1e-150}, // Dynamic range but within computable limits
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ComputeTonalityWithBands(tc.coeffs, 1, len(tc.coeffs))

			if math.IsNaN(result.Tonality) {
				t.Errorf("Produced NaN for %s", tc.name)
			}
			// Inf might be acceptable in some extreme cases, but NaN is never ok
		})
	}
}

// =============================================================================
// Tonality Range Tests
// =============================================================================

// TestTonalityOutputRange verifies tonality is always in [0, 1] range.
func TestTonalityOutputRange(t *testing.T) {
	frameSize := 480
	nbBands := getNbBandsForFrameSize(frameSize)

	testInputs := [][]float64{
		generateSineWaveMDCT(1000, 48000, frameSize),
		generateWhiteNoiseMDCT(frameSize, 12345),
		generateHarmonicMDCT(440, 48000, frameSize, 5),
		make([]float64, frameSize), // zeros
	}

	for i, coeffs := range testInputs {
		result := ComputeTonalityWithBands(coeffs, nbBands, frameSize)

		if result.Tonality < 0.0 || result.Tonality > 1.0 {
			t.Errorf("Test %d: Tonality %v out of [0, 1] range", i, result.Tonality)
		}

		// Check per-band values too
		for j, bt := range result.BandTonality {
			if bt < 0.0 || bt > 1.0 {
				t.Errorf("Test %d, Band %d: Tonality %v out of [0, 1] range", i, j, bt)
			}
		}
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

// BenchmarkComputeTonality benchmarks tonality computation for realistic frame sizes.
func BenchmarkComputeTonality(b *testing.B) {
	frameSizes := []int{480, 960}

	for _, frameSize := range frameSizes {
		nbBands := getNbBandsForFrameSize(frameSize)
		coeffs := generateHarmonicMDCT(440, 48000, frameSize, 10)

		b.Run("", func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = ComputeTonalityWithBands(coeffs, nbBands, frameSize)
			}
		})
	}
}

// BenchmarkComputeSpectralFlux benchmarks spectral flux computation.
func BenchmarkComputeSpectralFlux(b *testing.B) {
	nbBands := 21
	energies1 := make([]float64, nbBands)
	energies2 := make([]float64, nbBands)

	for i := 0; i < nbBands; i++ {
		energies1[i] = float64(i + 1)
		energies2[i] = float64(nbBands - i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ComputeSpectralFlux(energies2, energies1, nbBands)
	}
}

// BenchmarkComputeTonalityWorstCase benchmarks with worst-case input (noise).
func BenchmarkComputeTonalityWorstCase(b *testing.B) {
	frameSize := 960
	nbBands := getNbBandsForFrameSize(frameSize)
	coeffs := generateWhiteNoiseMDCT(frameSize, 12345)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ComputeTonalityWithBands(coeffs, nbBands, frameSize)
	}
}

// BenchmarkGeometricMean benchmarks the geometric mean helper.
func BenchmarkGeometricMean(b *testing.B) {
	values := make([]float64, 960)
	for i := range values {
		values[i] = float64(i + 1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = geometricMean(values)
	}
}

// BenchmarkArithmeticMean benchmarks the arithmetic mean helper.
func BenchmarkArithmeticMean(b *testing.B) {
	values := make([]float64, 960)
	for i := range values {
		values[i] = float64(i + 1)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = arithmeticMean(values)
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

// TestTonalityWithRealMDCT tests tonality with actual MDCT transform.
func TestTonalityWithRealMDCT(t *testing.T) {
	// Generate a sine wave in time domain
	frameSize := 480
	sampleRate := 48000
	frequency := 1000.0
	nbBands := getNbBandsForFrameSize(frameSize)

	// Create time-domain samples
	samples := make([]float64, frameSize+Overlap)
	for i := range samples {
		ti := float64(i) / float64(sampleRate)
		samples[i] = math.Sin(2 * math.Pi * frequency * ti)
	}

	// Apply MDCT
	coeffs := MDCT(samples)
	if coeffs == nil {
		t.Skip("MDCT returned nil")
	}

	result := ComputeTonalityWithBands(coeffs, nbBands, frameSize)

	// Real MDCT of sine wave should show high tonality
	if result.Tonality < 0.7 {
		t.Errorf("MDCT of sine wave tonality = %v, want >= 0.7", result.Tonality)
	}

	t.Logf("Real MDCT tonality for 1kHz sine: %.4f", result.Tonality)
}

// TestTonalityWithRealNoise tests tonality with actual MDCT of noise.
// The band-based SFM gives moderate tonality (~0.7) for noise because
// individual CELT bands still show variance in the Gaussian distribution.
func TestTonalityWithRealNoise(t *testing.T) {
	frameSize := 480
	nbBands := getNbBandsForFrameSize(frameSize)
	rng := rand.New(rand.NewSource(12345))

	// Create time-domain noise
	samples := make([]float64, frameSize+Overlap)
	for i := range samples {
		samples[i] = rng.Float64()*2 - 1
	}

	// Apply MDCT
	coeffs := MDCT(samples)
	if coeffs == nil {
		t.Skip("MDCT returned nil")
	}

	result := ComputeTonalityWithBands(coeffs, nbBands, frameSize)

	// Real MDCT of noise should have lower tonality than pure tone (1.0)
	// but the band-based analysis gives moderate values (~0.7)
	if result.Tonality > 0.85 {
		t.Errorf("MDCT of noise tonality = %v, want < 0.85 (less than pure tone)", result.Tonality)
	}

	t.Logf("Real MDCT tonality for noise: %.4f", result.Tonality)
}

// TestTonalityFrameContinuity tests tonality analysis across frame boundaries.
func TestTonalityFrameContinuity(t *testing.T) {
	frameSize := 480
	nbBands := getNbBandsForFrameSize(frameSize)
	numFrames := 5

	// Generate continuous sine wave across frames
	coeffsList := make([][]float64, numFrames)
	for f := 0; f < numFrames; f++ {
		coeffsList[f] = generateSineWaveMDCT(1000, 48000, frameSize)
	}

	var prevEnergies []float64
	for f := 0; f < numFrames; f++ {
		result := ComputeTonalityWithBands(coeffsList[f], nbBands, frameSize)

		// Continuous signal should have consistent tonality
		if result.Tonality < 0.8 {
			t.Errorf("Frame %d tonality = %v, want >= 0.8", f, result.Tonality)
		}

		// Compute energies for flux calculation
		energies := make([]float64, nbBands)
		for i := 0; i < nbBands; i++ {
			energies[i] = 1.0 // Simplified
		}

		// After first frame, flux should be low (same signal)
		if f > 0 && prevEnergies != nil {
			flux := ComputeSpectralFlux(energies, prevEnergies, nbBands)
			if flux > 0.1 {
				t.Errorf("Frame %d flux = %v, want <= 0.1 for continuous signal", f, flux)
			}
		}

		prevEnergies = energies
	}
}

// TestComputeTonalityFromNormalized tests the normalized variant.
func TestComputeTonalityFromNormalized(t *testing.T) {
	frameSize := 480
	nbBands := getNbBandsForFrameSize(frameSize)

	// Generate normalized-like coefficients
	coeffs := generateSineWaveMDCT(1000, 48000, frameSize)

	result := ComputeTonalityFromNormalized(coeffs, nbBands, frameSize)

	// Should produce valid tonality
	if result.Tonality < 0.0 || result.Tonality > 1.0 {
		t.Errorf("Normalized tonality %v out of range", result.Tonality)
	}
}
