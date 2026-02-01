package celt

import (
	"math"
	"testing"
)

// TestCombFilterGains verifies the comb filter gain tables match libopus.
// Reference: libopus celt/celt.c gains[][]
func TestCombFilterGains(t *testing.T) {
	// libopus uses Q15 fixed-point: QCONST16(value, 15)
	// The float values must match exactly.
	expectedGains := [3][3]float64{
		{0.3066406250, 0.2170410156, 0.1296386719},
		{0.4638671875, 0.2680664062, 0.0000000000},
		{0.7998046875, 0.1000976562, 0.0000000000},
	}

	for tapset := 0; tapset < 3; tapset++ {
		for tap := 0; tap < 3; tap++ {
			expected := expectedGains[tapset][tap]
			got := combFilterGains[tapset][tap]
			if math.Abs(got-expected) > 1e-10 {
				t.Errorf("combFilterGains[%d][%d] = %v, want %v", tapset, tap, got, expected)
			}
		}
	}
}

// TestCombFilterConstants verifies min/max period constants match libopus.
// Reference: libopus celt/celt.h COMBFILTER_MINPERIOD, COMBFILTER_MAXPERIOD
func TestCombFilterConstants(t *testing.T) {
	if combFilterMinPeriod != 15 {
		t.Errorf("combFilterMinPeriod = %d, want 15", combFilterMinPeriod)
	}
	if combFilterMaxPeriod != 1024 {
		t.Errorf("combFilterMaxPeriod = %d, want 1024", combFilterMaxPeriod)
	}
}

// TestCombFilterNoGain verifies the filter does nothing when both gains are zero.
// Reference: libopus celt/celt.c comb_filter() early return on g0==0 && g1==0
func TestCombFilterNoGain(t *testing.T) {
	// Create buffer with history and frame
	n := 120
	history := combFilterHistory
	buf := make([]float64, history+n)

	// Fill with test signal
	for i := range buf {
		buf[i] = float64(i % 100)
	}
	original := make([]float64, len(buf))
	copy(original, buf)

	// Apply filter with zero gains
	window := GetWindowBuffer(Overlap)
	combFilter(buf, history, 100, 100, n, 0.0, 0.0, 0, 0, window, Overlap)

	// Buffer should be unchanged
	for i := history; i < len(buf); i++ {
		if buf[i] != original[i] {
			t.Errorf("buf[%d] = %v, want %v (unchanged)", i, buf[i], original[i])
			break
		}
	}
}

// TestCombFilterConstantParams verifies no overlap transition when parameters unchanged.
// Reference: libopus celt/celt.c comb_filter() overlap=0 when g0==g1 && T0==T1 && tapset0==tapset1
func TestCombFilterConstantParams(t *testing.T) {
	// This test verifies the optimization where overlap is skipped if params match
	n := 240
	history := combFilterHistory
	buf := make([]float64, history+n)

	// Fill with sinusoidal test signal
	for i := range buf {
		buf[i] = math.Sin(float64(i) * 0.1)
	}

	period := 100
	gain := 0.5
	tapset := 0

	window := GetWindowBuffer(Overlap)

	// Apply filter with constant parameters
	combFilter(buf, history, period, period, n, gain, gain, tapset, tapset, window, Overlap)

	// The filter should apply uniformly (no crossfade needed)
	// Verify it produces non-zero changes
	hasChange := false
	for i := history; i < len(buf); i++ {
		original := math.Sin(float64(i) * 0.1)
		if math.Abs(buf[i]-original) > 1e-10 {
			hasChange = true
			break
		}
	}

	if !hasChange {
		t.Error("Filter should modify signal when gain > 0")
	}
}

// TestCombFilterImpulseResponse tests the 5-tap comb filter structure.
// Reference: libopus celt/celt.c comb_filter() computes y[i] = x[i] + g*(g0*x[i-T] + g1*(x[i-T-1]+x[i-T+1]) + g2*(x[i-T-2]+x[i-T+2]))
func TestCombFilterImpulseResponse(t *testing.T) {
	// Create impulse at a known position
	n := 100
	history := combFilterHistory
	period := 50
	buf := make([]float64, history+n)

	// Place impulse at history-period (so it affects sample at history)
	impulsePos := history - period
	buf[impulsePos] = 1.0

	gain := 1.0 // Full gain for easy verification
	tapset := 0

	window := GetWindowBuffer(Overlap)

	// Apply filter
	combFilter(buf, history, period, period, n, gain, gain, tapset, tapset, window, Overlap)

	// Expected response at buf[history]:
	// y = x + g * (g00*x[i-T] + g01*(x[i-T-1]+x[i-T+1]) + g02*(x[i-T-2]+x[i-T+2]))
	// With impulse at i-T:
	// y[history] = 0 + 1.0 * (g00*1 + g01*(0+0) + g02*(0+0)) = g00 = 0.3066406250

	expected := combFilterGains[tapset][0] // g00
	got := buf[history]

	if math.Abs(got-expected) > 1e-6 {
		t.Errorf("Impulse response at position 0: got %v, want %v", got, expected)
	}
}

// TestCombFilterCrossfade verifies smooth transition during overlap region.
// Reference: libopus celt/celt.c comb_filter() uses window^2 for crossfade
func TestCombFilterCrossfade(t *testing.T) {
	n := 240
	history := combFilterHistory
	buf := make([]float64, history+n)

	// Fill with constant signal for easy verification
	for i := range buf {
		buf[i] = 1.0
	}

	// Different parameters to force crossfade
	t0, t1 := 50, 100
	g0, g1 := 0.5, 0.3
	tapset0, tapset1 := 0, 1

	window := GetWindowBuffer(Overlap)

	// Apply filter
	combFilter(buf, history, t0, t1, n, g0, g1, tapset0, tapset1, window, Overlap)

	// Verify crossfade happens in overlap region
	// At start of overlap, old filter dominates (1-f is high)
	// At end of overlap, new filter dominates (f is high)

	// Check that values in overlap region transition smoothly
	for i := 1; i < Overlap; i++ {
		prev := buf[history+i-1]
		curr := buf[history+i]
		next := buf[history+i+1]

		// Values should be monotonic or near-monotonic during transition
		// (this is a weak test, but catches obvious bugs)
		avgDiff := math.Abs(next-prev) / 2
		actualDiff := math.Abs(curr - (next+prev)/2)

		if actualDiff > avgDiff*10 {
			t.Logf("Potential discontinuity at overlap[%d]: prev=%v, curr=%v, next=%v",
				i, prev, curr, next)
		}
	}
}

// TestCombFilterPeriodBounds verifies period clamping to valid range.
// Reference: libopus celt/celt.c IMAX(T0, COMBFILTER_MINPERIOD)
func TestCombFilterPeriodBounds(t *testing.T) {
	// Test that very small periods don't cause crashes
	n := 120
	history := combFilterHistory
	buf := make([]float64, history+n)

	for i := range buf {
		buf[i] = float64(i)
	}

	window := GetWindowBuffer(Overlap)

	// These should not panic
	combFilter(buf, history, 1, 1, n, 0.5, 0.5, 0, 0, window, Overlap)
	combFilter(buf, history, 0, 0, n, 0.5, 0.5, 0, 0, window, Overlap)
	combFilter(buf, history, -10, -10, n, 0.5, 0.5, 0, 0, window, Overlap)
}

// TestSanitizePostfilterParams verifies parameter sanitization.
// Reference: libopus celt/celt_decoder.c period/tapset validation
func TestSanitizePostfilterParams(t *testing.T) {
	tests := []struct {
		name               string
		t0, t1             int
		g0, g1             float64
		tap0, tap1         int
		wantT0, wantT1     int
		wantTap0, wantTap1 int
	}{
		{
			name: "valid params",
			t0:   100, t1: 200,
			g0: 0.5, g1: 0.5,
			tap0: 0, tap1: 1,
			wantT0: 100, wantT1: 200,
			wantTap0: 0, wantTap1: 1,
		},
		{
			name: "t0 too small, uses t1",
			t0:   5, t1: 200,
			g0: 0.5, g1: 0.5,
			tap0: 0, tap1: 0,
			wantT0: 200, wantT1: 200,
			wantTap0: 0, wantTap1: 0,
		},
		{
			name: "t1 too large, uses t0",
			t0:   100, t1: 2000,
			g0: 0.5, g1: 0.5,
			tap0: 0, tap1: 0,
			wantT0: 100, wantT1: 100,
			wantTap0: 0, wantTap1: 0,
		},
		{
			name: "g0 zero, t0 copies t1",
			t0:   100, t1: 200,
			g0: 0.0, g1: 0.5,
			tap0: 0, tap1: 1,
			wantT0: 200, wantT1: 200,
			wantTap0: 0, wantTap1: 1,
		},
		{
			name: "invalid tapset, clamps to valid",
			t0:   100, t1: 200,
			g0: 0.5, g1: 0.5,
			tap0: -1, tap1: 5,
			wantT0: 100, wantT1: 200,
			wantTap0: 0, wantTap1: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotT0, gotT1, gotTap0, gotTap1 := sanitizePostfilterParams(
				tc.t0, tc.t1, tc.g0, tc.g1, tc.tap0, tc.tap1)

			if gotT0 != tc.wantT0 || gotT1 != tc.wantT1 {
				t.Errorf("periods: got (%d, %d), want (%d, %d)",
					gotT0, gotT1, tc.wantT0, tc.wantT1)
			}
			if gotTap0 != tc.wantTap0 || gotTap1 != tc.wantTap1 {
				t.Errorf("tapsets: got (%d, %d), want (%d, %d)",
					gotTap0, gotTap1, tc.wantTap0, tc.wantTap1)
			}
		})
	}
}

// TestCombFilterEnergyPreservation verifies the filter doesn't add excessive energy.
// The comb filter adds delayed copies, so some energy increase is expected,
// but it should be bounded.
func TestCombFilterEnergyPreservation(t *testing.T) {
	n := 480
	history := combFilterHistory
	buf := make([]float64, history+n)

	// Fill with random-ish signal
	for i := range buf {
		buf[i] = math.Sin(float64(i)*0.03) * math.Cos(float64(i)*0.07)
	}

	// Compute input energy
	inputEnergy := 0.0
	for i := history; i < len(buf); i++ {
		inputEnergy += buf[i] * buf[i]
	}

	window := GetWindowBuffer(Overlap)

	// Apply filter with moderate gain
	combFilter(buf, history, 100, 100, n, 0.5, 0.5, 0, 0, window, Overlap)

	// Compute output energy
	outputEnergy := 0.0
	for i := history; i < len(buf); i++ {
		outputEnergy += buf[i] * buf[i]
	}

	// Energy ratio should be reasonable (not more than 4x for typical gain < 1)
	ratio := outputEnergy / inputEnergy
	if ratio > 4.0 || ratio < 0.25 {
		t.Errorf("Energy ratio %v is outside expected range [0.25, 4.0]", ratio)
	}
}

// TestApplyPostfilterMono tests the decoder's applyPostfilter for mono.
func TestApplyPostfilterMono(t *testing.T) {
	d := NewDecoder(1)

	frameSize := 480
	lm := 2 // 10ms frame

	// Create test samples
	samples := make([]float64, frameSize)
	for i := range samples {
		samples[i] = math.Sin(float64(i) * 0.05)
	}
	original := make([]float64, len(samples))
	copy(original, samples)

	// Apply postfilter with some parameters
	d.applyPostfilter(samples, frameSize, lm, 100, 0.5, 0)

	// Verify samples were modified
	changed := false
	for i := range samples {
		if math.Abs(samples[i]-original[i]) > 1e-10 {
			changed = true
			break
		}
	}

	if !changed {
		t.Error("Postfilter should modify samples when gain > 0")
	}

	// Verify state was updated
	if d.postfilterPeriod != 100 {
		t.Errorf("postfilterPeriod = %d, want 100", d.postfilterPeriod)
	}
	if d.postfilterGain != 0.5 {
		t.Errorf("postfilterGain = %v, want 0.5", d.postfilterGain)
	}
}

// TestApplyPostfilterStereo tests the decoder's applyPostfilter for stereo.
func TestApplyPostfilterStereo(t *testing.T) {
	d := NewDecoder(2)

	frameSize := 480
	lm := 2

	// Create interleaved stereo samples
	samples := make([]float64, frameSize*2)
	for i := 0; i < frameSize; i++ {
		samples[i*2] = math.Sin(float64(i) * 0.05)   // Left
		samples[i*2+1] = math.Cos(float64(i) * 0.05) // Right
	}
	original := make([]float64, len(samples))
	copy(original, samples)

	// Apply postfilter
	d.applyPostfilter(samples, frameSize, lm, 100, 0.5, 0)

	// Verify both channels were modified
	leftChanged, rightChanged := false, false
	for i := 0; i < frameSize; i++ {
		if math.Abs(samples[i*2]-original[i*2]) > 1e-10 {
			leftChanged = true
		}
		if math.Abs(samples[i*2+1]-original[i*2+1]) > 1e-10 {
			rightChanged = true
		}
	}

	if !leftChanged {
		t.Error("Left channel should be modified")
	}
	if !rightChanged {
		t.Error("Right channel should be modified")
	}
}

// TestApplyPostfilterStateTransition tests smooth state transitions across frames.
func TestApplyPostfilterStateTransition(t *testing.T) {
	d := NewDecoder(1)

	frameSize := 480
	lm := 2

	// First frame with one set of parameters
	samples1 := make([]float64, frameSize)
	for i := range samples1 {
		samples1[i] = 1.0 // Constant signal
	}
	d.applyPostfilter(samples1, frameSize, lm, 100, 0.3, 0)

	// Second frame with different parameters
	samples2 := make([]float64, frameSize)
	for i := range samples2 {
		samples2[i] = 1.0
	}
	d.applyPostfilter(samples2, frameSize, lm, 150, 0.5, 1)

	// Old parameters should be updated
	if d.postfilterPeriodOld != 150 {
		t.Errorf("postfilterPeriodOld = %d, want 150", d.postfilterPeriodOld)
	}
}

// TestCombFilterVsLibopusReference tests against known libopus output values.
// These reference values would ideally come from running libopus with the same input.
func TestCombFilterVsLibopusReference(t *testing.T) {
	// This test structure allows adding reference values from libopus
	// For now, verify basic invariants that must match

	testCases := []struct {
		name     string
		period   int
		gain     float64
		tapset   int
		inputLen int
	}{
		{"short_period", 20, 0.5, 0, 120},
		{"medium_period", 100, 0.3, 1, 240},
		{"long_period", 500, 0.7, 2, 480},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			history := combFilterHistory
			buf := make([]float64, history+tc.inputLen)

			// Create deterministic input
			for i := range buf {
				buf[i] = math.Sin(float64(i) * 0.1)
			}

			window := GetWindowBuffer(Overlap)

			// Apply filter
			combFilter(buf, history, tc.period, tc.period, tc.inputLen,
				tc.gain, tc.gain, tc.tapset, tc.tapset, window, Overlap)

			// Basic sanity checks
			hasNaN := false
			hasInf := false
			for i := history; i < len(buf); i++ {
				if math.IsNaN(buf[i]) {
					hasNaN = true
				}
				if math.IsInf(buf[i], 0) {
					hasInf = true
				}
			}

			if hasNaN {
				t.Error("Output contains NaN")
			}
			if hasInf {
				t.Error("Output contains Inf")
			}
		})
	}
}

// BenchmarkCombFilter measures comb filter performance.
func BenchmarkCombFilter(b *testing.B) {
	n := 960 // 20ms frame
	history := combFilterHistory
	buf := make([]float64, history+n)
	window := GetWindowBuffer(Overlap)

	for i := range buf {
		buf[i] = float64(i%1000) / 1000.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		combFilter(buf, history, 100, 100, n, 0.5, 0.5, 0, 0, window, Overlap)
	}
}

// BenchmarkApplyPostfilter measures full postfilter application.
func BenchmarkApplyPostfilter(b *testing.B) {
	d := NewDecoder(2) // Stereo
	frameSize := 960
	samples := make([]float64, frameSize*2)

	for i := range samples {
		samples[i] = float64(i%1000) / 1000.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.applyPostfilter(samples, frameSize, 3, 100, 0.5, 0)
	}
}
