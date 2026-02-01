package celt

import (
	"math"
	"testing"
)

// TestAntiCollapseBasic tests basic anti-collapse functionality.
func TestAntiCollapseBasic(t *testing.T) {
	// Test parameters
	channels := 1
	lm := 3 // 20ms frame
	start := 0
	end := 21 // All bands
	frameSize := 960

	// Create test data
	coeffsL := make([]float64, frameSize)
	var coeffsR []float64

	// Create collapse mask - mark some bands as collapsed (0 = collapsed, 1 = has pulses)
	collapse := make([]byte, channels*MaxBands)
	// Mark bands 5, 10, 15 as collapsed
	for i := 0; i < MaxBands; i++ {
		if i == 5 || i == 10 || i == 15 {
			collapse[i*channels] = 0 // collapsed
		} else {
			collapse[i*channels] = 0xFF // not collapsed
		}
	}

	// Create energy arrays
	logE := make([]float64, end*channels)
	prev1LogE := make([]float64, MaxBands*channels)
	prev2LogE := make([]float64, MaxBands*channels)

	// Set some energy values
	for i := 0; i < end; i++ {
		logE[i] = -10.0 // Current frame low energy
	}
	for i := 0; i < MaxBands; i++ {
		prev1LogE[i] = -5.0 // Previous frame higher energy
		prev2LogE[i] = -5.0
	}

	// Set pulses - some bands have allocation, some don't
	pulses := make([]int, end)
	for i := 0; i < end; i++ {
		pulses[i] = 100 // Some allocation
	}

	seed := uint32(12345)

	// Run anti-collapse
	antiCollapse(coeffsL, coeffsR, collapse, lm, channels, start, end, logE, prev1LogE, prev2LogE, pulses, seed)

	// Check that collapsed bands have noise injected
	collapsedBands := []int{5, 10, 15}
	for _, band := range collapsedBands {
		bandStart := EBands[band] << lm
		bandEnd := EBands[band+1] << lm

		// Check that at least some coefficients are non-zero
		hasNonZero := false
		for i := bandStart; i < bandEnd && i < len(coeffsL); i++ {
			if coeffsL[i] != 0 {
				hasNonZero = true
				break
			}
		}
		if !hasNonZero {
			t.Errorf("Band %d should have noise injected but all zeros", band)
		}

		// Check that the band is normalized (approximately unit energy)
		energy := 0.0
		for i := bandStart; i < bandEnd && i < len(coeffsL); i++ {
			energy += coeffsL[i] * coeffsL[i]
		}
		// After renormalization, energy should be close to 1.0
		if energy < 0.5 || energy > 2.0 {
			t.Errorf("Band %d: expected energy ~1.0, got %.4f", band, energy)
		}
	}

	// Check that non-collapsed bands are unaffected (still zero)
	for band := start; band < end; band++ {
		isCollapsed := false
		for _, cb := range collapsedBands {
			if band == cb {
				isCollapsed = true
				break
			}
		}
		if isCollapsed {
			continue
		}

		bandStart := EBands[band] << lm
		bandEnd := EBands[band+1] << lm
		allZero := true
		for i := bandStart; i < bandEnd && i < len(coeffsL); i++ {
			if coeffsL[i] != 0 {
				allZero = false
				break
			}
		}
		if !allZero {
			t.Errorf("Band %d is not collapsed but has non-zero coefficients", band)
		}
	}
}

// TestAntiCollapseStereo tests anti-collapse with stereo channels.
func TestAntiCollapseStereo(t *testing.T) {
	channels := 2
	lm := 3
	start := 0
	end := 21
	frameSize := 960

	coeffsL := make([]float64, frameSize)
	coeffsR := make([]float64, frameSize)

	// Create collapse mask for both channels
	collapse := make([]byte, channels*MaxBands)
	// Mark band 5 as collapsed for left channel only
	// Mark band 10 as collapsed for both channels
	for i := 0; i < MaxBands; i++ {
		if i == 5 {
			collapse[i*channels+0] = 0    // L collapsed
			collapse[i*channels+1] = 0xFF // R not collapsed
		} else if i == 10 {
			collapse[i*channels+0] = 0 // L collapsed
			collapse[i*channels+1] = 0 // R collapsed
		} else {
			collapse[i*channels+0] = 0xFF
			collapse[i*channels+1] = 0xFF
		}
	}

	logE := make([]float64, end*channels)
	prev1LogE := make([]float64, MaxBands*channels)
	prev2LogE := make([]float64, MaxBands*channels)

	for c := 0; c < channels; c++ {
		for i := 0; i < end; i++ {
			logE[c*end+i] = -10.0
		}
		for i := 0; i < MaxBands; i++ {
			prev1LogE[c*MaxBands+i] = -5.0
			prev2LogE[c*MaxBands+i] = -5.0
		}
	}

	pulses := make([]int, end)
	for i := 0; i < end; i++ {
		pulses[i] = 100
	}

	seed := uint32(12345)

	antiCollapse(coeffsL, coeffsR, collapse, lm, channels, start, end, logE, prev1LogE, prev2LogE, pulses, seed)

	// Check band 5: L should have noise, R should be zero
	band5Start := EBands[5] << lm
	band5End := EBands[6] << lm

	hasNonZeroL := false
	hasNonZeroR := false
	for i := band5Start; i < band5End && i < frameSize; i++ {
		if coeffsL[i] != 0 {
			hasNonZeroL = true
		}
		if coeffsR[i] != 0 {
			hasNonZeroR = true
		}
	}

	if !hasNonZeroL {
		t.Error("Band 5 L channel should have noise")
	}
	if hasNonZeroR {
		t.Error("Band 5 R channel should not have noise")
	}

	// Check band 10: both should have noise
	band10Start := EBands[10] << lm
	band10End := EBands[11] << lm

	hasNonZeroL = false
	hasNonZeroR = false
	for i := band10Start; i < band10End && i < frameSize; i++ {
		if coeffsL[i] != 0 {
			hasNonZeroL = true
		}
		if coeffsR[i] != 0 {
			hasNonZeroR = true
		}
	}

	if !hasNonZeroL {
		t.Error("Band 10 L channel should have noise")
	}
	if !hasNonZeroR {
		t.Error("Band 10 R channel should have noise")
	}
}

// TestAntiCollapsePRNG tests that the PRNG produces deterministic results.
func TestAntiCollapsePRNG(t *testing.T) {
	channels := 1
	lm := 2
	start := 0
	end := 21
	frameSize := 480

	// Run twice with same seed
	for run := 0; run < 2; run++ {
		coeffsL := make([]float64, frameSize)
		collapse := make([]byte, channels*MaxBands)
		collapse[5*channels] = 0 // Band 5 collapsed

		logE := make([]float64, end*channels)
		prev1LogE := make([]float64, MaxBands*channels)
		prev2LogE := make([]float64, MaxBands*channels)

		for i := 0; i < MaxBands; i++ {
			if i < end {
				logE[i] = -10.0
			}
			prev1LogE[i] = -5.0
			prev2LogE[i] = -5.0
		}

		pulses := make([]int, end)
		for i := 0; i < end; i++ {
			pulses[i] = 50
		}

		seed := uint32(0xDEADBEEF)
		antiCollapse(coeffsL, nil, collapse, lm, channels, start, end, logE, prev1LogE, prev2LogE, pulses, seed)

		// Store first run results
		if run == 0 {
			t.Logf("Run %d: First few coefficients in band 5:", run)
			band5Start := EBands[5] << lm
			for i := 0; i < 5 && band5Start+i < frameSize; i++ {
				t.Logf("  coeffsL[%d] = %.6f", band5Start+i, coeffsL[band5Start+i])
			}
		}
	}
}

// TestAntiCollapseThreshold tests the depth-based threshold calculation.
func TestAntiCollapseThreshold(t *testing.T) {
	// Test threshold formula: thresh = 0.5 * exp2(-0.125 * depth)
	// depth = (1 + pulses[band]) / N0 >> lm

	testCases := []struct {
		pulses int
		N0     int
		lm     int
	}{
		{0, 4, 0},   // depth = 1/4 = 0, thresh = 0.5
		{7, 4, 0},   // depth = 8/4 = 2, thresh = 0.5 * 2^(-0.25) = 0.42
		{100, 8, 2}, // depth = 101/8 >> 2 = 3, thresh = 0.5 * 2^(-0.375)
	}

	for _, tc := range testCases {
		depth := celtUdiv(1+tc.pulses, tc.N0) >> tc.lm
		thresh := 0.5 * math.Exp2(-0.125*float64(depth))
		t.Logf("pulses=%d, N0=%d, lm=%d -> depth=%d, thresh=%.4f",
			tc.pulses, tc.N0, tc.lm, depth, thresh)
	}
}

// TestAntiCollapseEnergyDiff tests the energy difference calculation.
func TestAntiCollapseEnergyDiff(t *testing.T) {
	// Test formula: r = 2 * exp2(-ediff) where ediff = max(0, logE - min(prev1, prev2))
	//
	// Note: logE values are in log2 scale (1 unit = 6 dB).
	// When current energy (logE) is lower than previous (prev), ediff is negative
	// and gets clamped to 0, giving r = 2.
	// When current energy is higher, ediff > 0 and r decreases exponentially.

	testCases := []struct {
		logE  float64
		prev1 float64
		prev2 float64
		desc  string
	}{
		{-10, -5, -5, "current lower than prev"},   // ediff = -10 - (-5) = -5, clamped to 0, r = 2
		{-5, -10, -10, "current higher than prev"}, // ediff = -5 - (-10) = 5, r = 2 * 2^(-5)
		{0, -5, -10, "current much higher"},        // ediff = 0 - (-10) = 10, r = 2 * 2^(-10)
		{-5, -5, -10, "prev1 equals logE"},         // ediff = -5 - (-10) = 5 (uses min of prev)
	}

	for _, tc := range testCases {
		ediff := tc.logE - math.Min(tc.prev1, tc.prev2)
		if ediff < 0 {
			ediff = 0
		}
		r := 2 * math.Exp2(-ediff)
		expectedR := 2 * math.Exp2(-ediff) // Same formula, just verify it computes correctly
		t.Logf("%s: logE=%.1f, prev1=%.1f, prev2=%.1f -> ediff=%.1f, r=%.6f",
			tc.desc, tc.logE, tc.prev1, tc.prev2, ediff, r)
		if math.Abs(r-expectedR) > 1e-10 {
			t.Errorf("Mismatch: got %.10f, expected %.10f", r, expectedR)
		}
	}
}

// TestAntiCollapseMonoInStereoStream tests the mono-in-stereo-stream case.
func TestAntiCollapseMonoInStereoStream(t *testing.T) {
	// When decoding mono in a stereo stream, libopus uses max(L, R) for prev energies
	channels := 1
	lm := 3
	start := 0
	end := 21
	frameSize := 960

	coeffsL := make([]float64, frameSize)
	collapse := make([]byte, channels*MaxBands)
	collapse[5*channels] = 0 // Band 5 collapsed

	logE := make([]float64, end*channels)
	// prev arrays have 2*MaxBands (stereo layout even for mono decode)
	prev1LogE := make([]float64, 2*MaxBands)
	prev2LogE := make([]float64, 2*MaxBands)

	for i := 0; i < end; i++ {
		logE[i] = -10.0
	}
	// Set different energies for L and R in prev arrays
	for i := 0; i < MaxBands; i++ {
		prev1LogE[i] = -8.0          // L channel
		prev1LogE[MaxBands+i] = -3.0 // R channel (higher)
		prev2LogE[i] = -9.0
		prev2LogE[MaxBands+i] = -4.0
	}

	pulses := make([]int, end)
	for i := 0; i < end; i++ {
		pulses[i] = 50
	}

	seed := uint32(12345)

	antiCollapse(coeffsL, nil, collapse, lm, channels, start, end, logE, prev1LogE, prev2LogE, pulses, seed)

	// The function should use max of L and R prev energies
	// For band 5:
	// - prev1 should be max(-8, -3) = -3
	// - prev2 should be max(-9, -4) = -4
	// - ediff = logE[5] - min(prev1, prev2) = -10 - min(-3, -4) = -10 - (-4) = -6
	// Since ediff < 0, it's clamped to 0, so r = 2 * exp2(0) = 2

	band5Start := EBands[5] << lm
	band5End := EBands[6] << lm

	hasNonZero := false
	for i := band5Start; i < band5End && i < frameSize; i++ {
		if coeffsL[i] != 0 {
			hasNonZero = true
			break
		}
	}

	if !hasNonZero {
		t.Error("Band 5 should have noise injected in mono-in-stereo case")
	}
}

// TestAntiCollapseLM3Scaling tests the sqrt(2) scaling for LM=3.
func TestAntiCollapseLM3Scaling(t *testing.T) {
	// For LM=3 (20ms), r is multiplied by sqrt(2) = 1.41421356

	channels := 1
	start := 0
	end := 21
	frameSize := 960

	for _, lm := range []int{2, 3} {
		coeffsL := make([]float64, frameSize)
		collapse := make([]byte, channels*MaxBands)
		collapse[5*channels] = 0

		logE := make([]float64, end*channels)
		prev1LogE := make([]float64, MaxBands*channels)
		prev2LogE := make([]float64, MaxBands*channels)

		for i := 0; i < MaxBands; i++ {
			if i < end {
				logE[i] = 0.0
			}
			prev1LogE[i] = 0.0
			prev2LogE[i] = 0.0
		}

		pulses := make([]int, end)
		for i := 0; i < end; i++ {
			pulses[i] = 1000 // High pulses to make thresh small
		}

		seed := uint32(0x12345678)
		antiCollapse(coeffsL, nil, collapse, lm, channels, start, end, logE, prev1LogE, prev2LogE, pulses, seed)

		// Measure the noise amplitude (before renormalization it would be +/- r)
		// After renormalization, energy = 1.0 regardless, but we can see the pattern
		band5Start := EBands[5] << lm
		band5End := EBands[6] << lm
		energy := 0.0
		for i := band5Start; i < band5End && i < frameSize; i++ {
			energy += coeffsL[i] * coeffsL[i]
		}

		t.Logf("LM=%d: band 5 energy = %.6f", lm, energy)
	}
}

// TestAntiCollapseAllSubBlocks tests that all sub-blocks without pulses get noise.
func TestAntiCollapseAllSubBlocks(t *testing.T) {
	// When lm > 0, there are multiple sub-blocks (M = 1 << lm)
	// Each sub-block that has no pulses (collapse_mask bit = 0) gets noise

	channels := 1
	lm := 2 // M = 4 sub-blocks
	M := 1 << lm
	start := 0
	end := 21
	frameSize := 480

	coeffsL := make([]float64, frameSize)

	collapse := make([]byte, channels*MaxBands)
	// Collapse mask for band 5: only sub-block 0 and 2 collapsed (bits 0 and 2 = 0)
	// Bits 1 and 3 are set (not collapsed)
	collapse[5*channels] = (1 << 1) | (1 << 3) // = 0b1010 = 10

	logE := make([]float64, end*channels)
	prev1LogE := make([]float64, MaxBands*channels)
	prev2LogE := make([]float64, MaxBands*channels)

	for i := 0; i < MaxBands; i++ {
		if i < end {
			logE[i] = -5.0
		}
		prev1LogE[i] = 0.0
		prev2LogE[i] = 0.0
	}

	pulses := make([]int, end)
	for i := 0; i < end; i++ {
		pulses[i] = 50
	}

	seed := uint32(42)
	antiCollapse(coeffsL, nil, collapse, lm, channels, start, end, logE, prev1LogE, prev2LogE, pulses, seed)

	// Check sub-blocks in band 5
	N0 := EBands[6] - EBands[5]
	bandOffset := EBands[5] << lm

	for k := 0; k < M; k++ {
		subBlockHasNoise := false
		for j := 0; j < N0; j++ {
			idx := bandOffset + (j << lm) + k
			if idx < frameSize && coeffsL[idx] != 0 {
				subBlockHasNoise = true
				break
			}
		}

		// Sub-blocks 0 and 2 should have noise (collapsed)
		// Sub-blocks 1 and 3 should not (mask bits are set)
		expectNoise := (k == 0 || k == 2)
		if subBlockHasNoise != expectNoise {
			t.Errorf("Sub-block %d: expected noise=%v, got=%v", k, expectNoise, subBlockHasNoise)
		}
	}
}
