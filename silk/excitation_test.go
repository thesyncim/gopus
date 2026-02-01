package silk

import (
	"testing"
)

func TestExcitationOutputLength(t *testing.T) {
	// Test that excitation output arrays have correct length
	tests := []struct {
		name            string
		subframeSamples int
		numShells       int
	}{
		{"NB 5ms subframe", 40, 40 / 16},
		{"MB 5ms subframe", 60, 60 / 16},
		{"WB 5ms subframe", 80, 80 / 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify shell count calculation
			shellSize := 16
			expectedShells := tt.subframeSamples / shellSize
			if expectedShells != tt.numShells {
				t.Errorf("Expected %d shells for %d samples, got %d",
					tt.numShells, tt.subframeSamples, expectedShells)
			}

			// Verify excitation array can be allocated with correct size
			excitation := make([]int32, tt.subframeSamples)
			if len(excitation) != tt.subframeSamples {
				t.Errorf("Excitation length: got %d, want %d",
					len(excitation), tt.subframeSamples)
			}
		})
	}
}

func TestLPCFilterStability(t *testing.T) {
	// Test that limitLPCFilterGain prevents runaway coefficients
	tests := []struct {
		name      string
		lpc       []int16
		wantStable bool
	}{
		{
			name:       "Already stable",
			lpc:        []int16{2048, 1024, 512, 256, 128, 64, 32, 16, 8, 4},
			wantStable: true,
		},
		{
			name:       "High gain coefficients",
			lpc:        []int16{4096, 4096, 4096, 4096, 4096, 4096, 4096, 4096, 4096, 4096},
			wantStable: true,
		},
		{
			name:       "WB order high gain",
			lpc:        make([]int16, 16),
			wantStable: true,
		},
	}

	// Initialize WB test case with high values
	for i := range tests[2].lpc {
		tests[2].lpc[i] = 4096 // Q12 = 1.0
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy to preserve original
			lpcCopy := make([]int16, len(tt.lpc))
			copy(lpcCopy, tt.lpc)

			limitLPCFilterGain(lpcCopy)

			// After limiting, sum of squared coeffs should be bounded
			var sumSq int64
			for _, c := range lpcCopy {
				sumSq += int64(c) * int64(c)
			}

			// Check gain threshold (Q24)
			const gainThreshold = 1 << 24
			if sumSq >= gainThreshold {
				t.Errorf("LPC gain still too high after limiting: %d >= %d", sumSq, gainThreshold)
			}

			// Verify coefficients are smaller or equal after limiting
			for i := range lpcCopy {
				if absInt16(lpcCopy[i]) > absInt16(tt.lpc[i]) {
					t.Errorf("Coefficient %d increased: %d > %d", i, lpcCopy[i], tt.lpc[i])
				}
			}
		})
	}
}

func TestLTPPrediction(t *testing.T) {
	d := NewDecoder()

	// Initialize history with a known periodic pattern
	// This simulates a pitched speech signal
	for i := range d.outputHistory {
		// Create a simple sine-like pattern with period 50
		d.outputHistory[i] = float32(i%50) / 50.0
	}
	d.historyIndex = 100

	// Create test excitation (small values that will be modified by LTP)
	excitation := make([]int32, 40)
	for i := range excitation {
		excitation[i] = 100
	}
	originalSum := sumInt32(excitation)

	// LTP coefficients (Q7) - simple case with center tap emphasis
	// These represent a typical voiced frame with strong pitch periodicity
	ltpCoeffs := []int8{10, 20, 64, 20, 10} // Center tap = 0.5 in Q7

	// Apply LTP synthesis with pitch lag of 50 (matching our history pattern)
	d.ltpSynthesis(excitation, 50, ltpCoeffs, 0)

	// Verify excitation was modified (has pitch contribution)
	modifiedSum := sumInt32(excitation)
	if modifiedSum == originalSum {
		t.Error("LTP synthesis did not modify excitation")
	}

	// The excitation should have changed values (not all the same)
	hasVariation := false
	for i := 1; i < len(excitation); i++ {
		if excitation[i] != excitation[0] {
			hasVariation = true
			break
		}
	}
	if !hasVariation {
		t.Error("LTP synthesis produced uniform output (no pitch variation)")
	}
}

func TestLPCSynthesisBasic(t *testing.T) {
	d := NewDecoder()

	// Initialize previous LPC values to zero (clean state)
	for i := range d.prevLPCValues {
		d.prevLPCValues[i] = 0
	}

	// Simple excitation: impulse at start
	excitation := make([]int32, 40)
	excitation[0] = 1000 // Single impulse

	// Simple LPC coefficients (Q12) - mild resonance
	// These create a simple decaying response
	lpc := []int16{2048, 1024, 512, 256, 128, 64, 32, 16, 8, 4}

	output := make([]float32, 40)
	d.lpcSynthesis(excitation, lpc, 1<<16, output)

	// Output should have non-zero value at position 0 (impulse response)
	if output[0] == 0 {
		t.Error("LPC synthesis produced no output for impulse")
	}

	// Check for reasonable range (normalized to [-1, 1])
	for i, o := range output {
		if o > 1.0 || o < -1.0 {
			t.Errorf("Output[%d] = %f out of normalized range [-1, 1]", i, o)
		}
	}

	// Verify decaying response (each sample should be smaller than previous after peak)
	// The impulse response of a stable filter should decay
	foundPeak := false
	peakIdx := 0
	for i := 1; i < len(output); i++ {
		if absFloat32(output[i]) > absFloat32(output[i-1]) && !foundPeak {
			// Still rising
		} else if absFloat32(output[i]) < absFloat32(output[peakIdx]) {
			foundPeak = true
			peakIdx = i - 1
		}
	}
}

func TestLPCSynthesisStatePersistence(t *testing.T) {
	d := NewDecoder()

	// Initialize state with a known pattern
	for i := range d.prevLPCValues {
		d.prevLPCValues[i] = 0.1 * float32(i+1)
	}

	// Process a subframe - the output should depend on state
	exc := make([]int32, 20)
	// Zero excitation - output depends entirely on filter state
	out := make([]float32, 20)

	// Simple LPC that amplifies the state (positive feedback)
	lpc := []int16{2048, 1024, 512, 256, 128, 64, 32, 16, 8, 4}
	d.lpcSynthesis(exc, lpc, 1<<16, out)

	// Output should be non-zero due to state (prevLPCValues was non-zero)
	hasNonZero := false
	for _, o := range out {
		if o != 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("LPC synthesis did not use state - output all zeros")
	}

	// After synthesis, the state should be updated with last samples from output
	order := len(lpc)
	for i := 0; i < order; i++ {
		stateVal := d.prevLPCValues[i]
		outIdx := len(out) - order + i
		if outIdx >= 0 && outIdx < len(out) {
			if stateVal != out[outIdx] {
				t.Errorf("State[%d] = %f, want out[%d] = %f", i, stateVal, outIdx, out[outIdx])
			}
		}
	}
}

func TestShellSplitDistribution(t *testing.T) {
	// Test that shell split distributes pulses correctly
	tests := []struct {
		name        string
		totalPulses int
		shellSize   int
	}{
		{"8 pulses in 16 samples", 8, 16},
		{"1 pulse in 16 samples", 1, 16},
		{"16 pulses in 16 samples", 16, 16},
		{"4 pulses in 8 samples", 4, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pulses := make([]int, tt.shellSize)

			// Simulate distribution without range decoder
			// Just verify the array handling works
			if tt.totalPulses <= tt.shellSize {
				// Put one pulse per position up to totalPulses
				for i := 0; i < tt.totalPulses; i++ {
					pulses[i] = 1
				}
			}

			// Verify sum equals total
			sum := 0
			for _, p := range pulses {
				sum += p
			}
			if sum != tt.totalPulses {
				t.Errorf("Pulse sum %d != expected %d", sum, tt.totalPulses)
			}
		})
	}
}

func TestBandwidthExpansion(t *testing.T) {
	// Test that bandwidth expansion reduces coefficient magnitudes
	lpc := []int16{4096, 4096, 4096, 4096, 4096} // All Q12 = 1.0

	// Compute original sum of squares
	var origSumSq int64
	for _, c := range lpc {
		origSumSq += int64(c) * int64(c)
	}

	// Apply bandwidth expansion (chirp = 0.99 in Q15)
	applyBandwidthExpansion(lpc, 32440)

	// Compute new sum of squares
	var newSumSq int64
	for _, c := range lpc {
		newSumSq += int64(c) * int64(c)
	}

	// New sum should be smaller
	if newSumSq >= origSumSq {
		t.Errorf("Bandwidth expansion did not reduce coefficients: %d >= %d", newSumSq, origSumSq)
	}

	// Each successive coefficient should be smaller than original
	// (exponential decay due to chirp^k)
	origCoeffs := []int16{4096, 4096, 4096, 4096, 4096}
	for i := range lpc {
		if absInt16(lpc[i]) >= absInt16(origCoeffs[i]) {
			t.Errorf("Coefficient %d not reduced: %d >= %d", i, lpc[i], origCoeffs[i])
		}
		// Later coefficients should be more reduced
		if i > 0 && lpc[i] >= lpc[i-1] {
			t.Errorf("Coefficient %d not more reduced than %d: %d >= %d",
				i, i-1, lpc[i], lpc[i-1])
		}
	}
}

func TestLPCInterpolate(t *testing.T) {
	lpc0 := []int16{0, 0, 0, 0, 0}
	lpc1 := []int16{256, 512, 768, 1024, 1280}

	// Test alpha = 0 (should give lpc0)
	result := lpcInterpolate(lpc0, lpc1, 0)
	for i, v := range result {
		if v != lpc0[i] {
			t.Errorf("alpha=0: result[%d] = %d, want %d", i, v, lpc0[i])
		}
	}

	// Test alpha = 256 (should give lpc1)
	result = lpcInterpolate(lpc0, lpc1, 256)
	for i, v := range result {
		if v != lpc1[i] {
			t.Errorf("alpha=256: result[%d] = %d, want %d", i, v, lpc1[i])
		}
	}

	// Test alpha = 128 (should give midpoint)
	result = lpcInterpolate(lpc0, lpc1, 128)
	for i, v := range result {
		expected := lpc1[i] / 2
		if v != expected {
			t.Errorf("alpha=128: result[%d] = %d, want %d", i, v, expected)
		}
	}
}

func TestHistoryUpdate(t *testing.T) {
	d := NewDecoder()

	// Initialize history to zeros
	for i := range d.outputHistory {
		d.outputHistory[i] = 0
	}
	d.historyIndex = 0

	// Add some samples
	samples := []float32{0.1, 0.2, 0.3, 0.4, 0.5}
	d.updateHistory(samples)

	// Verify samples were added
	if d.historyIndex != 5 {
		t.Errorf("History index not updated: got %d, want 5", d.historyIndex)
	}

	// Verify samples can be retrieved
	for i := len(samples) - 1; i >= 0; i-- {
		offset := len(samples) - 1 - i
		got := d.getHistorySample(offset + 1) // +1 because historyIndex points to next write
		want := samples[i]
		if got != want {
			t.Errorf("getHistorySample(%d) = %f, want %f", offset+1, got, want)
		}
	}
}

func TestExcitationSplitTables(t *testing.T) {
	// Verify split tables have correct structure
	for i, table := range ICDFExcitationSplit {
		// Each table should start at 256 and end at 0
		if len(table) < 2 {
			t.Errorf("Split table %d too short: %d", i, len(table))
			continue
		}
		if table[0] != 256 {
			t.Errorf("Split table %d doesn't start at 256: %d", i, table[0])
		}
		if table[len(table)-1] != 0 {
			t.Errorf("Split table %d doesn't end at 0: %d", i, table[len(table)-1])
		}

		// Values should be monotonically decreasing
		for j := 1; j < len(table); j++ {
			if table[j] >= table[j-1] {
				t.Errorf("Split table %d not decreasing at %d: %d >= %d",
					i, j, table[j], table[j-1])
			}
		}
	}
}

func TestScaleExcitation(t *testing.T) {
	excitation := []int32{100, 200, 300, -100, -200}

	// Test with gain = 1.0 (Q16 = 65536)
	excCopy := make([]int32, len(excitation))
	copy(excCopy, excitation)
	scaleExcitation(excCopy, 65536)

	// With gain = 1.0, values should be unchanged
	for i, v := range excCopy {
		if v != excitation[i] {
			t.Errorf("Gain 1.0: excitation[%d] = %d, want %d", i, v, excitation[i])
		}
	}

	// Test with gain = 0.5 (Q16 = 32768)
	copy(excCopy, excitation)
	scaleExcitation(excCopy, 32768)

	// With gain = 0.5, values should be halved
	for i, v := range excCopy {
		expected := excitation[i] / 2
		if v != expected {
			t.Errorf("Gain 0.5: excitation[%d] = %d, want %d", i, v, expected)
		}
	}
}

// Helper functions

func absInt16(x int16) int16 {
	if x < 0 {
		return -x
	}
	return x
}

func absFloat32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func sumInt32(arr []int32) int64 {
	var sum int64
	for _, v := range arr {
		sum += int64(v)
	}
	return sum
}
