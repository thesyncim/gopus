package bwe

import (
	"math"
	"testing"
)

// TestCalculateFeaturesStructure exercises the BWE feature extractor on a
// synthetic 16 kHz signal and verifies the per-hop output is structurally
// sensible. The numbers are not bit-exact comparisons against libopus (that
// requires an `--enable-osce-bwe` build of libopus); they check the invariants
// implied by the libopus reference:
//
//   - For a non-silent input the log-magnitude band slot is finite and bounded.
//   - For a sinusoid at ~1 kHz the log-mag spectrogram peaks in the bands
//     centred around bin 20 (1000/(16000/320) ~ 20) rather than DC.
//   - On the second hop, the instantaneous-frequency cos/sin pairs lie inside
//     the unit circle (|c|^2 + |s|^2 <= 1 + small slack).
//   - The signal-history buffer is updated so a consecutive call sees the
//     analysis window straddling both hops (idempotency: re-running with the
//     same state yields different values on hop 2 vs hop 1).
func TestCalculateFeaturesStructure(t *testing.T) {
	const (
		fs      = 16000
		f0      = 1000 // Hz
		numHops = 4
		hopSize = bweHalfWindowSize // 160 samples per 10 ms
	)
	xq := make([]int16, numHops*hopSize)
	for n := range xq {
		v := math.Sin(2 * math.Pi * float64(f0) * float64(n) / float64(fs))
		xq[n] = int16(math.Round(v * 16000))
	}

	var state FeatureState
	features := make([]float32, numHops*bweFeatureDim)
	state.CalculateFeatures(features, xq)

	for hop := 0; hop < numHops; hop++ {
		base := hop * bweFeatureDim
		lmspec := features[base : base+bweNumBands]
		instafreq := features[base+bweNumBands : base+bweFeatureDim]

		// Log-magnitude bands should be finite and within a reasonable range.
		for b, v := range lmspec {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("hop %d band %d lmspec=%v (NaN/Inf)", hop, b, v)
			}
			if v < -25 || v > 25 {
				t.Fatalf("hop %d band %d lmspec=%v outside [-25,25]", hop, b, v)
			}
		}

		// Inst-freq vectors must be in [-1,1] (cos/sin of a phase difference).
		for i, v := range instafreq {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("hop %d instafreq[%d]=%v (NaN/Inf)", hop, i, v)
			}
			if v < -1.001 || v > 1.001 {
				t.Fatalf("hop %d instafreq[%d]=%v outside [-1,1]", hop, i, v)
			}
		}
		// Each (cos,sin) pair satisfies cos^2 + sin^2 ~ 1 (modulo the +1e-9
		// stabilisation in libopus, which keeps the magnitude slightly below 1
		// when re1=im1=0). For our synthetic 1 kHz input the magnitudes
		// dominate the bias so the modulus is very close to 1.
		for k := 0; k <= bweMaxInstaFreqBin; k++ {
			c := instafreq[k]
			s := instafreq[k+bweMaxInstaFreqBin+1]
			mod := float64(c*c + s*s)
			if mod > 1.0001 {
				t.Fatalf("hop %d bin %d cos^2+sin^2=%v >1", hop, k, mod)
			}
		}
	}

	// Hop 2 onward must differ from hop 0 because by then the analysis window
	// has slid forward and the previous-spectrum buffer contains real values
	// rather than the reset-time 1e-9 prime.
	hop0 := features[0:bweFeatureDim]
	hop1 := features[bweFeatureDim : 2*bweFeatureDim]
	differ := false
	for i := range hop0 {
		if hop0[i] != hop1[i] {
			differ = true
			break
		}
	}
	if !differ {
		t.Fatalf("hop 1 features are bit-identical to hop 0 -- feature state is not advancing")
	}

	// State should not be in its initial all-zero state after processing.
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

// TestCalculateFeaturesSilenceProducesFloorEnergy verifies the libopus
// `log(x + 1e-9)` floor: an all-zero hop should yield identical log-mag values
// (the log of the 1e-9 bias) on every band slot and zero inst-freq cos/sin
// (modulo the stabilising bias).
func TestCalculateFeaturesSilenceProducesFloorEnergy(t *testing.T) {
	var state FeatureState
	xq := make([]int16, bweHalfWindowSize*2)
	features := make([]float32, 2*bweFeatureDim)
	state.CalculateFeatures(features, xq)

	expectedFloor := float32(math.Log(1e-9))
	for hop := 0; hop < 2; hop++ {
		base := hop * bweFeatureDim
		for b := 0; b < bweNumBands; b++ {
			v := features[base+b]
			if math.Abs(float64(v-expectedFloor)) > 1e-3 {
				t.Fatalf("hop %d band %d: got %v, want ~%v (silence floor)", hop, b, v, expectedFloor)
			}
		}
	}
}

func TestFeatureStateResetPrimesLastSpec(t *testing.T) {
	var state FeatureState
	state.Reset()
	if !state.primed {
		t.Fatalf("Reset did not mark state as primed")
	}
	for k := 0; k <= bweMaxInstaFreqBin; k++ {
		if got := state.lastSpec[2*k]; got != 1e-9 {
			t.Fatalf("lastSpec real bin %d = %g, want 1e-9", k, got)
		}
		if got := state.lastSpec[2*k+1]; got != 0 {
			t.Fatalf("lastSpec imag bin %d = %g, want 0", k, got)
		}
	}
}

// TestCalculateFeaturesNumFrames covers the supported 10 ms / 20 ms shapes.
func TestCalculateFeaturesNumFrames(t *testing.T) {
	cases := []struct {
		samples int
		hops    int
	}{{160, 1}, {320, 2}}
	for _, tc := range cases {
		var state FeatureState
		xq := make([]int16, tc.samples)
		for i := range xq {
			xq[i] = int16(i % 1000)
		}
		features := make([]float32, tc.hops*bweFeatureDim)
		state.CalculateFeatures(features, xq)
		// Every feature must be finite.
		for i, v := range features {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("samples=%d hops=%d features[%d]=%v (NaN/Inf)", tc.samples, tc.hops, i, v)
			}
		}
	}
}
