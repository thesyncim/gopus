package silk

import (
	"math"
	"testing"
)

func TestDetectPitchVoicedSignal(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)

	// Generate voiced signal at 200 Hz (pitch period = 80 samples at 16kHz)
	pitchPeriod := config.SampleRate / 200 // 80 samples
	frameSamples := config.SubframeSamples * 4 // 320 samples for 20ms

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		// Sawtooth-like voiced waveform
		phase := float64(i%pitchPeriod) / float64(pitchPeriod)
		pcm[i] = float32(1.0-2.0*phase) * 10000
	}

	// Detect pitch
	numSubframes := 4
	pitchLags := enc.detectPitch(pcm, numSubframes)

	if len(pitchLags) != numSubframes {
		t.Fatalf("expected %d pitch lags, got %d", numSubframes, len(pitchLags))
	}

	// All lags should be close to pitch period
	for sf, lag := range pitchLags {
		// Allow 20% error due to edge effects and search granularity
		errorMargin := pitchPeriod / 5
		if errorMargin < 2 {
			errorMargin = 2
		}
		error := absInt(lag - pitchPeriod)
		if error > errorMargin {
			t.Errorf("subframe %d: detected lag %d, expected ~%d (error: %d)", sf, lag, pitchPeriod, error)
		}
	}
}

func TestDetectPitchNarrowband(t *testing.T) {
	enc := NewEncoder(BandwidthNarrowband)
	config := GetBandwidthConfig(BandwidthNarrowband)

	// Generate voiced signal at 150 Hz (pitch period = ~53 samples at 8kHz)
	pitchPeriod := config.SampleRate / 150 // ~53 samples
	frameSamples := config.SubframeSamples * 4 // 160 samples for 20ms

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		phase := float64(i%pitchPeriod) / float64(pitchPeriod)
		pcm[i] = float32(1.0-2.0*phase) * 10000
	}

	numSubframes := 4
	pitchLags := enc.detectPitch(pcm, numSubframes)

	if len(pitchLags) != numSubframes {
		t.Fatalf("expected %d pitch lags, got %d", numSubframes, len(pitchLags))
	}

	// Verify lags are within valid range
	for sf, lag := range pitchLags {
		if lag < config.PitchLagMin || lag > config.PitchLagMax {
			t.Errorf("subframe %d: lag %d out of valid range [%d, %d]",
				sf, lag, config.PitchLagMin, config.PitchLagMax)
		}
	}
}

func TestDownsample(t *testing.T) {
	signal := []float32{1, 2, 3, 4, 5, 6, 7, 8}

	ds := downsample(signal, 2)
	if len(ds) != 4 {
		t.Fatalf("expected 4 samples, got %d", len(ds))
	}

	// Average of pairs
	expected := []float32{1.5, 3.5, 5.5, 7.5}
	for i, v := range ds {
		if math.Abs(float64(v-expected[i])) > 0.01 {
			t.Errorf("ds[%d] = %f, expected %f", i, v, expected[i])
		}
	}
}

func TestDownsampleFactor4(t *testing.T) {
	signal := []float32{1, 2, 3, 4, 5, 6, 7, 8}

	ds := downsample(signal, 4)
	if len(ds) != 2 {
		t.Fatalf("expected 2 samples, got %d", len(ds))
	}

	// Average of groups of 4
	expected := []float32{2.5, 6.5}
	for i, v := range ds {
		if math.Abs(float64(v-expected[i])) > 0.01 {
			t.Errorf("ds[%d] = %f, expected %f", i, v, expected[i])
		}
	}
}

func TestDownsampleFactor1(t *testing.T) {
	signal := []float32{1, 2, 3, 4}

	ds := downsample(signal, 1)
	if len(ds) != 4 {
		t.Fatalf("expected 4 samples, got %d", len(ds))
	}

	// No downsampling should occur
	for i, v := range ds {
		if v != signal[i] {
			t.Errorf("ds[%d] = %f, expected %f", i, v, signal[i])
		}
	}
}

func TestAutocorrPitchSearch(t *testing.T) {
	// Generate periodic signal
	period := 40
	n := 320
	signal := make([]float32, n)
	for i := range signal {
		signal[i] = float32(math.Sin(2 * math.Pi * float64(i) / float64(period)))
	}

	lag := autocorrPitchSearch(signal, 20, 60)

	// Should find the period (or close to it)
	// Due to bias toward shorter lags and edge effects, allow some tolerance
	if lag < period-5 || lag > period+5 {
		t.Errorf("detected lag %d, expected ~%d", lag, period)
	}
}

func TestAutocorrPitchSearchEdgeCases(t *testing.T) {
	// Test with very short signal
	shortSignal := []float32{1, 2, 3, 4, 5}
	lag := autocorrPitchSearch(shortSignal, 1, 3)
	if lag < 1 || lag > 3 {
		t.Errorf("short signal: lag %d out of range [1, 3]", lag)
	}

	// Test with minLag > maxLag
	lag = autocorrPitchSearch(shortSignal, 10, 5)
	if lag != 10 {
		t.Errorf("expected minLag=10 when minLag > maxLag, got %d", lag)
	}
}

func TestQuantizeLTPCoeffs(t *testing.T) {
	// Test with known coefficients
	coeffs := []float64{0.5, 0.3, 0.1, -0.1, -0.05}

	quantized := quantizeLTPCoeffs(coeffs, true)

	if len(quantized) != 5 {
		t.Fatalf("expected 5 coefficients, got %d", len(quantized))
	}

	// Coefficients should be in reasonable range (Q7 format: -128 to 127)
	for i, c := range quantized {
		if c < -128 || c > 127 {
			t.Errorf("quantized[%d] = %d out of Q7 range", i, c)
		}
	}
}

func TestQuantizeLTPCoeffsZeroCoeffs(t *testing.T) {
	// Test with zero coefficients
	coeffs := []float64{0, 0, 0, 0, 0}

	quantized := quantizeLTPCoeffs(coeffs, false)

	if len(quantized) != 5 {
		t.Fatalf("expected 5 coefficients, got %d", len(quantized))
	}

	// Should still produce valid quantized values
	for i, c := range quantized {
		if c < -128 || c > 127 {
			t.Errorf("quantized[%d] = %d out of Q7 range", i, c)
		}
	}
}

func TestQuantizeLTPCoeffsLargeCoeffs(t *testing.T) {
	// Test with large coefficients (should clip to codebook range)
	coeffs := []float64{2.0, 1.5, 1.0, -1.0, -1.5}

	quantized := quantizeLTPCoeffs(coeffs, true)

	if len(quantized) != 5 {
		t.Fatalf("expected 5 coefficients, got %d", len(quantized))
	}

	// Should still produce valid quantized values
	for i, c := range quantized {
		if c < -128 || c > 127 {
			t.Errorf("quantized[%d] = %d out of Q7 range", i, c)
		}
	}
}

func TestAnalyzeLTP(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)

	// Generate voiced signal
	pitchPeriod := 80 // 200 Hz at 16kHz
	frameSamples := config.SubframeSamples * 4

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		phase := float64(i%pitchPeriod) / float64(pitchPeriod)
		pcm[i] = float32(1.0-2.0*phase) * 10000
	}

	pitchLags := []int{pitchPeriod, pitchPeriod, pitchPeriod, pitchPeriod}
	numSubframes := 4

	ltpCoeffs := enc.analyzeLTP(pcm, pitchLags, numSubframes)

	if len(ltpCoeffs) != numSubframes {
		t.Fatalf("expected %d LTP coefficient sets, got %d", numSubframes, len(ltpCoeffs))
	}

	for sf, coeffs := range ltpCoeffs {
		if len(coeffs) != 5 {
			t.Errorf("subframe %d: expected 5 taps, got %d", sf, len(coeffs))
		}
		// Verify coefficients are in Q7 range
		for tap, c := range coeffs {
			if c < -128 || c > 127 {
				t.Errorf("subframe %d tap %d: %d out of Q7 range", sf, tap, c)
			}
		}
	}
}

func TestDeterminePeriodicity(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)
	config := GetBandwidthConfig(BandwidthWideband)

	// Generate highly periodic voiced signal
	pitchPeriod := 80
	frameSamples := config.SubframeSamples * 4

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		pcm[i] = float32(math.Sin(2 * math.Pi * float64(i) / float64(pitchPeriod)))
	}

	pitchLags := []int{pitchPeriod, pitchPeriod, pitchPeriod, pitchPeriod}

	periodicity := enc.determinePeriodicity(pcm, pitchLags)

	// Highly periodic signal should result in high periodicity (2)
	if periodicity < 1 {
		t.Errorf("expected high periodicity (>=1) for periodic signal, got %d", periodicity)
	}
}

func TestFindLTPCodebookIndex(t *testing.T) {
	// Test with known codebook entry from LTPFilterHigh
	coeffs := make([]int8, 5)
	copy(coeffs, LTPFilterHigh[0][:])

	idx := findLTPCodebookIndex(coeffs, 2)
	if idx != 0 {
		t.Errorf("expected index 0 for first entry, got %d", idx)
	}

	// Test with middle entry
	copy(coeffs, LTPFilterHigh[15][:])
	idx = findLTPCodebookIndex(coeffs, 2)
	if idx != 15 {
		t.Errorf("expected index 15 for entry 15, got %d", idx)
	}

	// Test with low periodicity
	copy(coeffs, LTPFilterLow[3][:])
	idx = findLTPCodebookIndex(coeffs, 0)
	if idx != 3 {
		t.Errorf("expected index 3 for LTPFilterLow[3], got %d", idx)
	}

	// Test with mid periodicity
	copy(coeffs, LTPFilterMid[7][:])
	idx = findLTPCodebookIndex(coeffs, 1)
	if idx != 7 {
		t.Errorf("expected index 7 for LTPFilterMid[7], got %d", idx)
	}
}

func TestFindBestPitchContour(t *testing.T) {
	enc := NewEncoder(BandwidthWideband)

	// Test with constant pitch lags
	pitchLags := []int{100, 100, 100, 100}
	contours := make([][4]int8, len(PitchContourWB20ms))
	for i := range PitchContourWB20ms {
		contours[i] = PitchContourWB20ms[i]
	}

	contourIdx, baseLag := enc.findBestPitchContour(pitchLags, contours, 4)

	// Should find a contour and base lag close to 100
	if baseLag < 95 || baseLag > 105 {
		t.Errorf("expected base lag ~100, got %d", baseLag)
	}

	// Contour index should be valid
	if contourIdx < 0 || contourIdx >= len(contours) {
		t.Errorf("contour index %d out of valid range [0, %d)", contourIdx, len(contours))
	}
}

func TestPitchMinMax(t *testing.T) {
	if pitchMin(5, 10) != 5 {
		t.Error("pitchMin(5, 10) should be 5")
	}
	if pitchMin(10, 5) != 5 {
		t.Error("pitchMin(10, 5) should be 5")
	}
	if pitchMax(5, 10) != 10 {
		t.Error("pitchMax(5, 10) should be 10")
	}
	if pitchMax(10, 5) != 10 {
		t.Error("pitchMax(10, 5) should be 10")
	}
}
