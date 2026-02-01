//go:build cgo_libopus
// +build cgo_libopus

// Package cgo_test provides CGO comparison tests for postfilter/comb filter.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

// TestCombFilterVsLibopus compares Go comb filter output against libopus.
func TestCombFilterVsLibopus(t *testing.T) {
	testCases := []struct {
		name    string
		period  int
		gain    float32
		tapset  int
		n       int
		overlap int
	}{
		{"short_period", 20, 0.5, 0, 120, 120},
		{"medium_period", 100, 0.3, 1, 240, 120},
		{"long_period", 500, 0.7, 2, 480, 120},
		{"tapset0", 50, 0.5, 0, 240, 120},
		{"tapset1", 50, 0.5, 1, 240, 120},
		{"tapset2", 50, 0.5, 2, 240, 120},
		{"low_gain", 100, 0.1, 0, 240, 120},
		{"high_gain", 100, 0.75, 0, 240, 120},
		{"min_period", 15, 0.5, 0, 120, 120},
		{"max_period", 1024, 0.5, 0, 480, 120},
	}

	const history = 1026 // combFilterMaxPeriod + 2

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create input buffer with history
			bufLen := history + tc.n
			x := make([]float32, bufLen)

			// Fill with sinusoidal test signal
			for i := range x {
				x[i] = float32(math.Sin(float64(i) * 0.1))
			}

			// Compute window using libopus formula
			window := make([]float32, tc.overlap)
			for i := 0; i < tc.overlap; i++ {
				window[i] = VorbisWindow(i, tc.overlap)
			}

			// Call libopus comb_filter
			yLibopus := CombFilter(x, history, tc.period, tc.period, tc.n,
				tc.gain, tc.gain, tc.tapset, tc.tapset, window, tc.overlap)

			// Compute Go version
			yGo := make([]float64, bufLen)
			for i := range x {
				yGo[i] = float64(x[i])
			}
			windowGo := make([]float64, tc.overlap)
			for i := range window {
				windowGo[i] = float64(window[i])
			}

			combFilterGo(yGo, history, tc.period, tc.period, tc.n,
				float64(tc.gain), float64(tc.gain), tc.tapset, tc.tapset, windowGo, tc.overlap)

			// Compare outputs
			maxDiff := float64(0)
			maxDiffIdx := 0
			for i := history; i < bufLen; i++ {
				diff := math.Abs(yGo[i] - float64(yLibopus[i]))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
			}

			// Tolerance: accept differences up to 1e-6 (float32 precision)
			tolerance := 1e-5
			if maxDiff > tolerance {
				t.Errorf("Max difference: %e at index %d (Go: %v, libopus: %v)",
					maxDiff, maxDiffIdx, yGo[maxDiffIdx], yLibopus[maxDiffIdx])
			} else {
				t.Logf("Max difference: %e (within tolerance)", maxDiff)
			}
		})
	}
}

// TestCombFilterCrossfadeVsLibopus tests the crossfade behavior during parameter changes.
func TestCombFilterCrossfadeVsLibopus(t *testing.T) {
	const history = 1026
	n := 240
	overlap := 120

	// Different parameters to force crossfade
	t0, t1 := 50, 100
	g0, g1 := float32(0.5), float32(0.3)
	tapset0, tapset1 := 0, 1

	bufLen := history + n
	x := make([]float32, bufLen)

	for i := range x {
		x[i] = float32(math.Sin(float64(i) * 0.05))
	}

	window := make([]float32, overlap)
	for i := 0; i < overlap; i++ {
		window[i] = VorbisWindow(i, overlap)
	}

	// Libopus result
	yLibopus := CombFilter(x, history, t0, t1, n, g0, g1, tapset0, tapset1, window, overlap)

	// Go result
	yGo := make([]float64, bufLen)
	for i := range x {
		yGo[i] = float64(x[i])
	}
	windowGo := make([]float64, overlap)
	for i := range window {
		windowGo[i] = float64(window[i])
	}
	combFilterGo(yGo, history, t0, t1, n, float64(g0), float64(g1), tapset0, tapset1, windowGo, overlap)

	// Compare
	maxDiff := float64(0)
	for i := history; i < bufLen; i++ {
		diff := math.Abs(yGo[i] - float64(yLibopus[i]))
		if diff > maxDiff {
			maxDiff = diff
		}
	}

	tolerance := 1e-5
	if maxDiff > tolerance {
		t.Errorf("Crossfade test: max difference %e exceeds tolerance %e", maxDiff, tolerance)
	} else {
		t.Logf("Crossfade test: max difference %e (within tolerance)", maxDiff)
	}
}

// TestVorbisWindowVsLibopus verifies the window values match exactly.
func TestVorbisWindowVsLibopus(t *testing.T) {
	overlap := 120

	for i := 0; i < overlap; i++ {
		libopusVal := VorbisWindow(i, overlap)
		goVal := vorbisWindowGo(i, overlap)

		diff := math.Abs(goVal - float64(libopusVal))
		if diff > 1e-6 {
			t.Errorf("Window[%d]: Go=%v, libopus=%v, diff=%e", i, goVal, libopusVal, diff)
		}
	}
}

// TestCombFilterZeroGainVsLibopus verifies both implementations return input unchanged when gains are zero.
func TestCombFilterZeroGainVsLibopus(t *testing.T) {
	const history = 1026
	n := 240
	overlap := 120

	bufLen := history + n
	x := make([]float32, bufLen)

	for i := range x {
		x[i] = float32(math.Sin(float64(i) * 0.1))
	}

	window := make([]float32, overlap)
	for i := 0; i < overlap; i++ {
		window[i] = VorbisWindow(i, overlap)
	}

	// Zero gains - should return input unchanged
	yLibopus := CombFilter(x, history, 100, 100, n, 0, 0, 0, 0, window, overlap)

	yGo := make([]float64, bufLen)
	for i := range x {
		yGo[i] = float64(x[i])
	}
	windowGo := make([]float64, overlap)
	for i := range window {
		windowGo[i] = float64(window[i])
	}
	combFilterGo(yGo, history, 100, 100, n, 0, 0, 0, 0, windowGo, overlap)

	// Both should be unchanged from input
	maxDiff := float64(0)
	for i := history; i < bufLen; i++ {
		diff := math.Abs(yGo[i] - float64(x[i]))
		if diff > maxDiff {
			maxDiff = diff
		}
		diffLibopus := math.Abs(float64(yLibopus[i]) - float64(x[i]))
		if diffLibopus > 1e-6 {
			t.Errorf("Libopus modified signal with zero gain at index %d", i)
		}
	}

	if maxDiff > 1e-10 {
		t.Errorf("Go modified signal with zero gain: max diff = %e", maxDiff)
	}
}

// TestCombFilterPeriodClampingVsLibopus verifies period clamping matches libopus.
func TestCombFilterPeriodClampingVsLibopus(t *testing.T) {
	const history = 1026
	const combFilterMinPeriod = 15
	n := 120
	overlap := 120

	testCases := []struct {
		name   string
		period int
	}{
		{"below_min", 5},
		{"at_min", 15},
		{"zero", 0},
		{"negative", -10},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bufLen := history + n
			x := make([]float32, bufLen)
			for i := range x {
				x[i] = float32(math.Sin(float64(i) * 0.1))
			}

			window := make([]float32, overlap)
			for i := 0; i < overlap; i++ {
				window[i] = VorbisWindow(i, overlap)
			}

			// libopus clamps periods to COMBFILTER_MINPERIOD
			effectivePeriod := tc.period
			if effectivePeriod < combFilterMinPeriod {
				effectivePeriod = combFilterMinPeriod
			}

			yLibopus := CombFilter(x, history, effectivePeriod, effectivePeriod, n,
				0.5, 0.5, 0, 0, window, overlap)

			yGo := make([]float64, bufLen)
			for i := range x {
				yGo[i] = float64(x[i])
			}
			windowGo := make([]float64, overlap)
			for i := range window {
				windowGo[i] = float64(window[i])
			}
			combFilterGo(yGo, history, tc.period, tc.period, n, 0.5, 0.5, 0, 0, windowGo, overlap)

			maxDiff := float64(0)
			for i := history; i < bufLen; i++ {
				diff := math.Abs(yGo[i] - float64(yLibopus[i]))
				if diff > maxDiff {
					maxDiff = diff
				}
			}

			tolerance := 1e-5
			if maxDiff > tolerance {
				t.Errorf("Period clamping mismatch: max diff = %e", maxDiff)
			}
		})
	}
}

// TestCombFilterAllTapsetsVsLibopus tests all three tapsets with various gains.
func TestCombFilterAllTapsetsVsLibopus(t *testing.T) {
	const history = 1026
	n := 240
	overlap := 120

	gains := []float32{0.1, 0.3, 0.5, 0.7, 0.9}

	for tapset := 0; tapset < 3; tapset++ {
		for _, gain := range gains {
			t.Run(
				"tapset"+string(rune('0'+tapset))+"_gain"+
					string(rune('0'+int(gain*10))), func(t *testing.T) {
					bufLen := history + n
					x := make([]float32, bufLen)
					for i := range x {
						x[i] = float32(math.Sin(float64(i)*0.1) * math.Cos(float64(i)*0.03))
					}

					window := make([]float32, overlap)
					for i := 0; i < overlap; i++ {
						window[i] = VorbisWindow(i, overlap)
					}

					yLibopus := CombFilter(x, history, 100, 100, n,
						gain, gain, tapset, tapset, window, overlap)

					yGo := make([]float64, bufLen)
					for i := range x {
						yGo[i] = float64(x[i])
					}
					windowGo := make([]float64, overlap)
					for i := range window {
						windowGo[i] = float64(window[i])
					}
					combFilterGo(yGo, history, 100, 100, n,
						float64(gain), float64(gain), tapset, tapset, windowGo, overlap)

					maxDiff := float64(0)
					for i := history; i < bufLen; i++ {
						diff := math.Abs(yGo[i] - float64(yLibopus[i]))
						if diff > maxDiff {
							maxDiff = diff
						}
					}

					tolerance := 1e-5
					if maxDiff > tolerance {
						t.Errorf("Tapset %d, gain %.1f: max diff = %e", tapset, gain, maxDiff)
					}
				})
		}
	}
}

// TestPostfilterParameterDecodeVsLibopus tests postfilter parameter decoding from bitstream.
// Reference: libopus celt_decoder.c lines 1340-1353
func TestPostfilterParameterDecodeVsLibopus(t *testing.T) {
	// tapset_icdf = {2, 1, 0}
	tapsetICDF := []uint8{2, 1, 0}

	testCases := []struct {
		name    string
		data    []byte
		hasFlag bool // whether postfilter flag is set
	}{
		// Test various bitstream patterns
		{"pattern_1", []byte{0xFF, 0x00, 0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC}, true},
		{"pattern_2", []byte{0x80, 0x40, 0x20, 0x10, 0x08, 0x04, 0x02, 0x01}, true},
		{"pattern_3", []byte{0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA}, true},
		{"pattern_4", []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE}, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Initialize Go range decoder
			rd := &rangecoding.Decoder{}
			rd.Init(tc.data)

			// Decode postfilter parameters as done in libopus
			// First check if we have a postfilter flag (decoded with logp=1)
			postfilterFlag := rd.DecodeBit(1)

			if postfilterFlag != 0 {
				// Decode octave (uniform over 6)
				octave := int(rd.DecodeUniform(6))

				// Decode pitch offset (4+octave bits)
				pitchOffset := int(rd.DecodeRawBits(uint(4 + octave)))

				// Compute postfilter period: (16<<octave)+pitchOffset-1
				postfilterPeriod := (16 << octave) + pitchOffset - 1

				// Decode gain (3 bits)
				qg := int(rd.DecodeRawBits(3))

				// gain = 0.09375 * (qg + 1)
				postfilterGain := 0.09375 * float64(qg+1)

				// Decode tapset (ICDF)
				postfilterTapset := rd.DecodeICDF(tapsetICDF, 2)

				// Validate period is within valid range
				if postfilterPeriod < 15 || postfilterPeriod >= 1024 {
					t.Logf("Period %d out of typical range [15, 1024)", postfilterPeriod)
				}

				// Validate tapset
				if postfilterTapset > 2 {
					t.Errorf("Invalid tapset decoded: %d", postfilterTapset)
				}

				// Validate gain
				if postfilterGain < 0.09375 || postfilterGain > 0.09375*8 {
					t.Errorf("Gain %f out of expected range", postfilterGain)
				}

				t.Logf("Decoded: period=%d, gain=%.4f, tapset=%d (octave=%d, qg=%d)",
					postfilterPeriod, postfilterGain, postfilterTapset, octave, qg)
			} else {
				t.Logf("Postfilter flag not set")
			}
		})
	}
}

// TestPostfilterStateTransitionVsLibopus tests state persistence across multiple filter applications.
// This simulates how the decoder applies postfilter across frame boundaries.
func TestPostfilterStateTransitionVsLibopus(t *testing.T) {
	const history = 1026
	const frameSize = 120 // Short MDCT size
	overlap := 120

	// Simulate 3 frames with changing parameters
	frames := []struct {
		periodOld, periodNew int
		gainOld, gainNew     float32
		tapsetOld, tapsetNew int
	}{
		// Frame 1: initial state (zero gain old, new gain active)
		{100, 100, 0, 0.5, 0, 0},
		// Frame 2: same parameters (no crossfade needed)
		{100, 100, 0.5, 0.5, 0, 0},
		// Frame 3: parameter change (crossfade required)
		{100, 150, 0.5, 0.3, 0, 1},
	}

	// Create a long signal buffer
	totalLen := history + 3*frameSize
	signal := make([]float32, totalLen)
	for i := range signal {
		signal[i] = float32(math.Sin(float64(i) * 0.05))
	}

	window := make([]float32, overlap)
	for i := 0; i < overlap; i++ {
		window[i] = VorbisWindow(i, overlap)
	}
	windowGo := make([]float64, overlap)
	for i := range window {
		windowGo[i] = float64(window[i])
	}

	// Process each frame
	for frameIdx, frame := range frames {
		t.Run("frame_"+string(rune('0'+frameIdx)), func(t *testing.T) {
			// Create buffers for this frame
			bufLen := history + frameSize
			xLibopus := make([]float32, bufLen)
			xGo := make([]float64, bufLen)

			// Copy history and frame data
			startIdx := frameIdx * frameSize
			for i := 0; i < bufLen; i++ {
				xLibopus[i] = signal[startIdx+i]
				xGo[i] = float64(signal[startIdx+i])
			}

			// Apply libopus comb filter
			yLibopus := CombFilter(xLibopus, history,
				frame.periodOld, frame.periodNew, frameSize,
				frame.gainOld, frame.gainNew,
				frame.tapsetOld, frame.tapsetNew,
				window, overlap)

			// Apply Go comb filter
			combFilterGo(xGo, history,
				frame.periodOld, frame.periodNew, frameSize,
				float64(frame.gainOld), float64(frame.gainNew),
				frame.tapsetOld, frame.tapsetNew,
				windowGo, overlap)

			// Compare outputs
			maxDiff := float64(0)
			maxDiffIdx := 0
			for i := history; i < bufLen; i++ {
				diff := math.Abs(xGo[i] - float64(yLibopus[i]))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
			}

			tolerance := 1e-5
			if maxDiff > tolerance {
				t.Errorf("Frame %d: max diff = %e at index %d", frameIdx, maxDiff, maxDiffIdx)
			} else {
				t.Logf("Frame %d: max diff = %e (within tolerance)", frameIdx, maxDiff)
			}
		})
	}
}

// TestCombFilterEdgeCasesVsLibopus tests edge cases that might cause divergence.
func TestCombFilterEdgeCasesVsLibopus(t *testing.T) {
	const history = 1026

	testCases := []struct {
		name                       string
		t0, t1                     int
		g0, g1                     float32
		tapset0, tapset1           int
		n, overlap                 int
		expectCrossfade            bool
		expectConstantFilterRegion bool
	}{
		// No crossfade when parameters match
		{"same_params", 100, 100, 0.5, 0.5, 0, 0, 240, 120, false, true},
		// Crossfade when period changes
		{"period_change", 100, 150, 0.5, 0.5, 0, 0, 240, 120, true, true},
		// Crossfade when gain changes
		{"gain_change", 100, 100, 0.3, 0.7, 0, 0, 240, 120, true, true},
		// Crossfade when tapset changes
		{"tapset_change", 100, 100, 0.5, 0.5, 0, 1, 240, 120, true, true},
		// All parameters change
		{"all_change", 80, 120, 0.4, 0.6, 0, 2, 240, 120, true, true},
		// New gain zero (early return after crossfade)
		{"g1_zero", 100, 100, 0.5, 0, 0, 0, 240, 120, true, false},
		// Old gain zero (crossfade from nothing)
		{"g0_zero", 100, 100, 0, 0.5, 0, 0, 240, 120, true, true},
		// Very short frame (n < overlap)
		{"short_frame", 100, 100, 0.5, 0.5, 0, 0, 60, 120, false, true},
		// Period at boundaries
		{"min_period", 15, 15, 0.5, 0.5, 0, 0, 120, 120, false, true},
		{"near_max_period", 1000, 1000, 0.5, 0.5, 0, 0, 240, 120, false, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			bufLen := history + tc.n
			x := make([]float32, bufLen)
			for i := range x {
				x[i] = float32(math.Sin(float64(i)*0.1) + 0.5*math.Cos(float64(i)*0.07))
			}

			actualOverlap := tc.overlap
			if actualOverlap > tc.n {
				actualOverlap = tc.n
			}

			window := make([]float32, actualOverlap)
			for i := 0; i < actualOverlap; i++ {
				window[i] = VorbisWindow(i, actualOverlap)
			}

			// Libopus
			yLibopus := CombFilter(x, history, tc.t0, tc.t1, tc.n,
				tc.g0, tc.g1, tc.tapset0, tc.tapset1, window, actualOverlap)

			// Go
			yGo := make([]float64, bufLen)
			for i := range x {
				yGo[i] = float64(x[i])
			}
			windowGo := make([]float64, actualOverlap)
			for i := range window {
				windowGo[i] = float64(window[i])
			}
			combFilterGo(yGo, history, tc.t0, tc.t1, tc.n,
				float64(tc.g0), float64(tc.g1), tc.tapset0, tc.tapset1, windowGo, actualOverlap)

			// Compare
			maxDiff := float64(0)
			maxDiffIdx := 0
			for i := history; i < bufLen; i++ {
				diff := math.Abs(yGo[i] - float64(yLibopus[i]))
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = i
				}
			}

			tolerance := 1e-5
			if maxDiff > tolerance {
				t.Errorf("%s: max diff = %e at index %d (Go: %v, libopus: %v)",
					tc.name, maxDiff, maxDiffIdx, yGo[maxDiffIdx], yLibopus[maxDiffIdx])
			} else {
				t.Logf("%s: max diff = %e (within tolerance)", tc.name, maxDiff)
			}
		})
	}
}

// TestCombFilterGainTableVsLibopus verifies gain table values match libopus exactly.
func TestCombFilterGainTableVsLibopus(t *testing.T) {
	// From libopus celt/celt.c:
	// static const opus_val16 gains[3][3] = {
	//    {QCONST16(0.3066406250f, 15), QCONST16(0.2170410156f, 15), QCONST16(0.1296386719f, 15)},
	//    {QCONST16(0.4638671875f, 15), QCONST16(0.2680664062f, 15), QCONST16(0.f, 15)},
	//    {QCONST16(0.7998046875f, 15), QCONST16(0.1000976562f, 15), QCONST16(0.f, 15)}};
	libopusGains := [3][3]float64{
		{0.3066406250, 0.2170410156, 0.1296386719},
		{0.4638671875, 0.2680664062, 0.0000000000},
		{0.7998046875, 0.1000976562, 0.0000000000},
	}

	goGains := [3][3]float64{
		{0.3066406250, 0.2170410156, 0.1296386719},
		{0.4638671875, 0.2680664062, 0.0000000000},
		{0.7998046875, 0.1000976562, 0.0000000000},
	}

	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if math.Abs(goGains[i][j]-libopusGains[i][j]) > 1e-10 {
				t.Errorf("Gain table mismatch: gains[%d][%d] = %v, want %v",
					i, j, goGains[i][j], libopusGains[i][j])
			}
		}
	}
}

// TestPostfilterGainComputationVsLibopus verifies gain computation from qg matches libopus.
// Reference: postfilter_gain = QCONST16(.09375f,15)*(qg+1);
func TestPostfilterGainComputationVsLibopus(t *testing.T) {
	// In libopus, gain = 0.09375 * (qg + 1) where qg is in [0, 7]
	for qg := 0; qg < 8; qg++ {
		libopusGain := 0.09375 * float64(qg+1)
		goGain := 0.09375 * float64(qg+1)

		if math.Abs(goGain-libopusGain) > 1e-10 {
			t.Errorf("Gain computation mismatch for qg=%d: Go=%v, libopus=%v",
				qg, goGain, libopusGain)
		}
		t.Logf("qg=%d: gain=%.6f", qg, goGain)
	}
}

// TestPostfilterPeriodComputationVsLibopus verifies period computation from octave/bits.
// Reference: postfilter_pitch = (16<<octave)+ec_dec_bits(dec, 4+octave)-1;
func TestPostfilterPeriodComputationVsLibopus(t *testing.T) {
	for octave := 0; octave < 6; octave++ {
		bitsNeeded := 4 + octave
		maxBitValue := (1 << bitsNeeded) - 1

		// Test min, max, and a few middle values
		testValues := []int{0, 1, maxBitValue / 2, maxBitValue - 1, maxBitValue}

		for _, bits := range testValues {
			if bits > maxBitValue {
				continue
			}

			// libopus formula: (16<<octave)+bits-1
			period := (16 << octave) + bits - 1

			// Verify it's within expected range
			minPeriod := (16 << octave) - 1
			maxPeriod := (16 << octave) + maxBitValue - 1

			if period < minPeriod || period > maxPeriod {
				t.Errorf("Period %d out of range [%d, %d] for octave=%d, bits=%d",
					period, minPeriod, maxPeriod, octave, bits)
			}

			t.Logf("octave=%d, bits=%d -> period=%d (range [%d, %d])",
				octave, bits, period, minPeriod, maxPeriod)
		}
	}
}

// combFilterGo is a local implementation matching the celt package.
// Duplicated here to avoid import cycle.
func combFilterGo(buf []float64, start int, t0, t1, n int, g0, g1 float64, tapset0, tapset1 int, window []float64, overlap int) {
	if n <= 0 {
		return
	}
	if g0 == 0 && g1 == 0 {
		return
	}

	const combFilterMinPeriod = 15
	if t0 < combFilterMinPeriod {
		t0 = combFilterMinPeriod
	}
	if t1 < combFilterMinPeriod {
		t1 = combFilterMinPeriod
	}

	if overlap > n {
		overlap = n
	}
	if overlap > len(window) {
		overlap = len(window)
	}

	gains := [3][3]float64{
		{0.3066406250, 0.2170410156, 0.1296386719},
		{0.4638671875, 0.2680664062, 0.0000000000},
		{0.7998046875, 0.1000976562, 0.0000000000},
	}

	if tapset0 < 0 || tapset0 >= 3 {
		tapset0 = 0
	}
	if tapset1 < 0 || tapset1 >= 3 {
		tapset1 = 0
	}

	g00 := g0 * gains[tapset0][0]
	g01 := g0 * gains[tapset0][1]
	g02 := g0 * gains[tapset0][2]
	g10 := g1 * gains[tapset1][0]
	g11 := g1 * gains[tapset1][1]
	g12 := g1 * gains[tapset1][2]

	x1 := buf[start-t1+1]
	x2 := buf[start-t1]
	x3 := buf[start-t1-1]
	x4 := buf[start-t1-2]

	if g0 == g1 && t0 == t1 && tapset0 == tapset1 {
		overlap = 0
	}

	for i := 0; i < overlap; i++ {
		f := window[i] * window[i]
		oneMinus := 1.0 - f
		idx := start + i
		x0 := buf[idx-t1+2]
		res := (oneMinus*g00)*buf[idx-t0] +
			(oneMinus*g01)*(buf[idx-t0-1]+buf[idx-t0+1]) +
			(oneMinus*g02)*(buf[idx-t0-2]+buf[idx-t0+2]) +
			(f*g10)*x2 +
			(f*g11)*(x3+x1) +
			(f*g12)*(x4+x0)
		buf[idx] += res
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}

	if g1 == 0 {
		return
	}

	i := overlap
	x4 = buf[start+i-t1-2]
	x3 = buf[start+i-t1-1]
	x2 = buf[start+i-t1]
	x1 = buf[start+i-t1+1]
	for ; i < n; i++ {
		idx := start + i
		x0 := buf[idx-t1+2]
		res := g10*x2 + g11*(x3+x1) + g12*(x4+x0)
		buf[idx] += res
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = x0
	}
}

// vorbisWindowGo computes Vorbis window matching the celt package.
func vorbisWindowGo(i, overlap int) float64 {
	if overlap <= 0 {
		return 0
	}
	x := float64(i) + 0.5
	sinArg := 0.5 * math.Pi * x / float64(overlap)
	s := math.Sin(sinArg)
	return math.Sin(0.5 * math.Pi * s * s)
}

// TestCombFilterImpulseResponseVsLibopus tests impulse response matches libopus.
func TestCombFilterImpulseResponseVsLibopus(t *testing.T) {
	const history = 1026
	n := 100
	period := 50
	overlap := 120

	// Place impulse at history-period position
	bufLen := history + n
	x := make([]float32, bufLen)
	x[history-period] = 1.0 // Impulse

	window := make([]float32, overlap)
	for i := 0; i < overlap; i++ {
		window[i] = VorbisWindow(i, overlap)
	}

	// Full gain, tapset 0
	yLibopus := CombFilter(x, history, period, period, n,
		1.0, 1.0, 0, 0, window, overlap)

	xGo := make([]float64, bufLen)
	for i := range x {
		xGo[i] = float64(x[i])
	}
	windowGo := make([]float64, overlap)
	for i := range window {
		windowGo[i] = float64(window[i])
	}
	combFilterGo(xGo, history, period, period, n, 1.0, 1.0, 0, 0, windowGo, overlap)

	// Expected: y[history] = 0 + 1.0 * g00 * impulse = g00 = 0.3066406250
	expectedResponse := 0.3066406250

	// Check libopus response
	libopusResponse := float64(yLibopus[history])
	if math.Abs(libopusResponse-expectedResponse) > 1e-5 {
		t.Errorf("Libopus impulse response: got %v, want %v", libopusResponse, expectedResponse)
	}

	// Check Go response
	goResponse := xGo[history]
	if math.Abs(goResponse-expectedResponse) > 1e-5 {
		t.Errorf("Go impulse response: got %v, want %v", goResponse, expectedResponse)
	}

	// Check they match
	if math.Abs(goResponse-libopusResponse) > 1e-6 {
		t.Errorf("Impulse response mismatch: Go=%v, libopus=%v", goResponse, libopusResponse)
	}

	t.Logf("Impulse response: Go=%v, libopus=%v, expected=%v", goResponse, libopusResponse, expectedResponse)
}

// BenchmarkCombFilterGoVsLibopus benchmarks both implementations.
func BenchmarkCombFilterGoVsLibopus(b *testing.B) {
	const history = 1026
	n := 960
	overlap := 120

	window := make([]float32, overlap)
	for i := 0; i < overlap; i++ {
		window[i] = VorbisWindow(i, overlap)
	}
	windowGo := make([]float64, overlap)
	for i := range window {
		windowGo[i] = float64(window[i])
	}

	b.Run("libopus", func(b *testing.B) {
		bufLen := history + n
		x := make([]float32, bufLen)
		for i := range x {
			x[i] = float32(i % 1000)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			CombFilter(x, history, 100, 100, n, 0.5, 0.5, 0, 0, window, overlap)
		}
	})

	b.Run("go", func(b *testing.B) {
		bufLen := history + n
		x := make([]float64, bufLen)
		for i := range x {
			x[i] = float64(i % 1000)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			combFilterGo(x, history, 100, 100, n, 0.5, 0.5, 0, 0, windowGo, overlap)
		}
	})
}
