package lace

import (
	"math"
	"testing"
)

// TestCalculateFeaturesStructure exercises the LACE feature extractor on a
// synthetic 16 kHz signal and verifies the per-subframe output is structurally
// sensible. The numbers are not bit-exact comparisons against libopus (a
// full parity probe requires an OSCE-enabled libopus build); they check the
// invariants implied by the libopus reference:
//
//   - Clean LPC log-spectrum slot is finite and bounded.
//   - Noisy cepstrum slot is finite.
//   - Auto-correlation values lie in [-1.001, 1.001] (normalised cross-corr).
//   - LTP coefficients reproduce the Q14 inputs after the (1/2^14) scaling.
//   - The log-gain slot equals log(gain) for the supplied Q16 gains.
//   - Successive subframes 0 and 2 produce updated values, while 1 and 3
//     copy from the preceding even subframe (libopus update-every-other rule).
//   - State (signal_history, numbits_smooth) advances after one call.
func TestCalculateFeaturesStructure(t *testing.T) {
	const (
		fs = 16000
		f0 = 1000 // Hz
	)
	xq := make([]int16, FrameSize)
	for n := range xq {
		v := math.Sin(2 * math.Pi * float64(f0) * float64(n) / float64(fs))
		xq[n] = int16(math.Round(v * 16000))
	}

	var ctrl FeatureControl
	ctrl.LPCOrder = 16
	// A trivial LPC: a1 = -0.5, others zero. PredCoefQ12[0] = -2048 (Q12).
	ctrl.PredCoefQ12[0][0] = -2048
	ctrl.PredCoefQ12[1][0] = -2048
	// LTP coefficients: monotonically increasing pattern to verify the
	// per-tap copy.
	for sf := range SubframesPerFrame {
		for tap := range ltpLen {
			ctrl.LTPCoefQ14[sf*ltpLen+tap] = int16(100 * (sf + 1) * (tap + 1))
		}
	}
	// Gains: 1.0, 2.0, 4.0, 8.0 in Q16.
	for sf := range SubframesPerFrame {
		ctrl.GainsQ16[sf] = int32(1<<16) << uint(sf)
		ctrl.PitchL[sf] = 80
	}
	ctrl.SignalType = typeVoiced

	var state FeatureState
	features := make([]float32, SubframesPerFrame*FeatureDim)
	numbits := make([]float32, 2)
	periods := make([]int, SubframesPerFrame)

	if !state.CalculateFeatures(features, numbits, periods, xq, &ctrl, 800) {
		t.Fatalf("CalculateFeatures returned false on a valid input")
	}

	// numbits[0] is the raw value; numbits[1] is the smoothed value.
	if numbits[0] != 800 {
		t.Fatalf("numbits[0]: got %v, want 800", numbits[0])
	}
	if math.Abs(float64(numbits[1])-80.0) > 1e-3 {
		// State starts at 0 and EWMA is 0.9*0 + 0.1*800 = 80.
		t.Fatalf("numbits[1]: got %v, want ~80 (0.9*0 + 0.1*800)", numbits[1])
	}

	for sf := range SubframesPerFrame {
		base := sf * FeatureDim
		// Clean spectrum (64 floats).
		for b := range cleanSpecLength {
			v := features[base+cleanSpecStart+b]
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("sf %d clean[%d]=%v NaN/Inf", sf, b, v)
			}
			if v < -20 || v > 20 {
				t.Fatalf("sf %d clean[%d]=%v outside [-20,20]", sf, b, v)
			}
		}
		// Noisy cepstrum (18 floats).
		for c := range noisyCepstrumLen {
			v := features[base+noisyCepstrumStart+c]
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("sf %d ceps[%d]=%v NaN/Inf", sf, c, v)
			}
		}
		// Acorr (5 floats) - normalised cross-correlation must be in [-1,1].
		for i := range acorrLen {
			v := features[base+acorrStart+i]
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("sf %d acorr[%d]=%v NaN/Inf", sf, i, v)
			}
			if v < -1.001 || v > 1.001 {
				t.Fatalf("sf %d acorr[%d]=%v outside [-1,1]", sf, i, v)
			}
		}
		// LTP (5 floats) - must round-trip the Q14 inputs.
		for tap := range ltpLen {
			got := features[base+ltpStart+tap]
			want := float32(ctrl.LTPCoefQ14[sf*ltpLen+tap]) / float32(1<<14)
			if math.Abs(float64(got-want)) > 1e-7 {
				t.Fatalf("sf %d ltp[%d]: got %v want %v", sf, tap, got, want)
			}
		}
		// Log-gain: log(2^sf + 1e-9). Gain = 2^sf in linear.
		gain := float32(uint32(1) << uint(sf))
		want := float32(math.Log(float64(gain) + 1e-9))
		got := features[base+logGainStart]
		if math.Abs(float64(got-want)) > 1e-5 {
			t.Fatalf("sf %d logGain: got %v want %v", sf, got, want)
		}
	}

	// Update-every-other rule: subframe 1 mirrors subframe 0's clean spec
	// and cepstrum slots verbatim.
	for b := range cleanSpecLength {
		v0 := features[0*FeatureDim+cleanSpecStart+b]
		v1 := features[1*FeatureDim+cleanSpecStart+b]
		if v0 != v1 {
			t.Fatalf("update-every-other broken on clean[%d]: sf0=%v sf1=%v", b, v0, v1)
		}
	}
	for c := range noisyCepstrumLen {
		v0 := features[0*FeatureDim+noisyCepstrumStart+c]
		v1 := features[1*FeatureDim+noisyCepstrumStart+c]
		if v0 != v1 {
			t.Fatalf("update-every-other broken on ceps[%d]: sf0=%v sf1=%v", c, v0, v1)
		}
	}

	// Voiced signal type: periods must be the raw pitch lags (80 each).
	for sf := range SubframesPerFrame {
		if periods[sf] != 80 {
			t.Fatalf("voiced periods[%d]: got %d want 80", sf, periods[sf])
		}
	}

	// State must advance: signalHistory is no longer all zeros.
	zero := true
	for _, v := range state.signalHistory {
		if v != 0 {
			zero = false
			break
		}
	}
	if zero {
		t.Fatalf("signal_history is still zero after CalculateFeatures")
	}
}

// TestCalculateFeaturesUnvoicedSubstitutesNoPitch verifies the
// pitch_postprocessing substitution: an unvoiced/inactive frame returns
// OSCE_NO_PITCH_VALUE (7) regardless of the raw pitch lag.
func TestCalculateFeaturesUnvoicedSubstitutesNoPitch(t *testing.T) {
	xq := make([]int16, FrameSize)
	for n := range xq {
		xq[n] = int16(n % 1000)
	}

	var ctrl FeatureControl
	ctrl.LPCOrder = 16
	ctrl.SignalType = typeUnvoiced
	for sf := range SubframesPerFrame {
		ctrl.PitchL[sf] = 80
		ctrl.GainsQ16[sf] = 1 << 16
	}

	var state FeatureState
	features := make([]float32, SubframesPerFrame*FeatureDim)
	numbits := make([]float32, 2)
	periods := make([]int, SubframesPerFrame)

	if !state.CalculateFeatures(features, numbits, periods, xq, &ctrl, 400) {
		t.Fatalf("CalculateFeatures returned false on a valid input")
	}

	for sf := range SubframesPerFrame {
		if periods[sf] != noPitchValue {
			t.Fatalf("unvoiced periods[%d]: got %d want %d (OSCE_NO_PITCH_VALUE)", sf, periods[sf], noPitchValue)
		}
	}
}

// TestCalculateFeaturesNumbitsSmoothing verifies the 0.9/0.1 EWMA on
// numbits[1] across two successive frames.
func TestCalculateFeaturesNumbitsSmoothing(t *testing.T) {
	xq := make([]int16, FrameSize)
	var ctrl FeatureControl
	ctrl.LPCOrder = 16
	for sf := range SubframesPerFrame {
		ctrl.GainsQ16[sf] = 1 << 16
	}

	var state FeatureState
	features := make([]float32, SubframesPerFrame*FeatureDim)
	numbits := make([]float32, 2)
	periods := make([]int, SubframesPerFrame)

	state.CalculateFeatures(features, numbits, periods, xq, &ctrl, 1000)
	if math.Abs(float64(numbits[1])-100.0) > 1e-3 {
		t.Fatalf("after first call numbits[1]: got %v, want 100 (0.9*0 + 0.1*1000)", numbits[1])
	}
	state.CalculateFeatures(features, numbits, periods, xq, &ctrl, 1000)
	// 0.9*100 + 0.1*1000 = 190.
	if math.Abs(float64(numbits[1])-190.0) > 1e-3 {
		t.Fatalf("after second call numbits[1]: got %v, want 190 (0.9*100 + 0.1*1000)", numbits[1])
	}
}

// TestResetClearsHistory verifies Reset zero-initialises persistent fields.
func TestResetClearsHistory(t *testing.T) {
	xq := make([]int16, FrameSize)
	for n := range xq {
		xq[n] = int16(n)
	}
	var ctrl FeatureControl
	ctrl.LPCOrder = 16
	for sf := range SubframesPerFrame {
		ctrl.GainsQ16[sf] = 1 << 16
	}

	var state FeatureState
	features := make([]float32, SubframesPerFrame*FeatureDim)
	numbits := make([]float32, 2)
	periods := make([]int, SubframesPerFrame)
	state.CalculateFeatures(features, numbits, periods, xq, &ctrl, 500)
	if state.numbitsSmooth == 0 {
		t.Fatalf("numbitsSmooth did not update after first call")
	}
	state.Reset()
	if state.numbitsSmooth != 0 {
		t.Fatalf("Reset did not zero numbitsSmooth: %v", state.numbitsSmooth)
	}
	if !state.reset {
		t.Fatalf("Reset did not set the reset flag")
	}
	for i, v := range state.signalHistory {
		if v != 0 {
			t.Fatalf("Reset left signalHistory[%d]=%v", i, v)
		}
	}
}
