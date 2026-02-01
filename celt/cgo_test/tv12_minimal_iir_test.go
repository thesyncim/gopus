//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests the IIR resampler behavior with controlled inputs.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/silk"
)

// TestMinimalIIRBehavior tests IIR filter with exact states from the comparison test.
func TestMinimalIIRBehavior(t *testing.T) {
	// Generate input that's similar to actual packet 826 input (small values throughout)
	// The actual input from comparison test: [0.000122, 0.000366, 0.000336, ...]
	// These are very small values, so generate similar small random-ish values
	input := make([]float32, 160)
	for i := range input {
		// Create small varying values similar to actual SILK output
		input[i] = float32((i%20)-10) * 0.00005 // Range: -0.0005 to +0.00045
	}
	// Override first 10 to match exact values from comparison test
	input[0] = 0.000122
	input[1] = 0.000366
	input[2] = 0.000336
	input[3] = 0.000183
	input[4] = 0.000153
	input[5] = 0.000092
	input[6] = 0.000153
	input[7] = 0.000458
	input[8] = 0.000458
	input[9] = 0.000305

	// Create two resamplers for NB->48kHz
	res1 := silk.NewLibopusResampler(8000, 48000)
	res2 := silk.NewLibopusResampler(8000, 48000)

	// Verify initial state
	t.Logf("Initial res1 sIIR: %v", res1.GetSIIR())
	t.Logf("Initial res2 sIIR: %v", res2.GetSIIR())
	t.Logf("Initial res1 delayBuf: %v", res1.GetDelayBuf())
	t.Logf("Initial res2 delayBuf: %v", res2.GetDelayBuf())

	// Set res2 to have the non-zero state from the comparison test
	res2.SetSIIR([6]int32{-630071, -83139, -749660, -440000, -330000, -220000})

	// Set res2 delayBuf to match the comparison test
	delayBuf := res2.GetDelayBuf()
	if len(delayBuf) >= 8 {
		delayBuf[0] = 80
		delayBuf[1] = 53
		delayBuf[2] = -20
		delayBuf[3] = 69
		delayBuf[4] = 21
		delayBuf[5] = -3
		delayBuf[6] = 41
		delayBuf[7] = 16
	}

	t.Logf("\nAfter setup:")
	t.Logf("res1 sIIR: %v", res1.GetSIIR())
	t.Logf("res2 sIIR: %v", res2.GetSIIR())
	t.Logf("res1 delayBuf: %v", res1.GetDelayBuf())
	t.Logf("res2 delayBuf: %v", res2.GetDelayBuf())

	// Process through both
	res1.EnableDebug(true)
	res2.EnableDebug(true)

	out1 := res1.Process(input)
	out2 := res2.Process(input)

	t.Logf("\nAfter Process():")
	t.Logf("res1 Process start sIIR: %v", res1.GetDebugProcessCallSIIR())
	t.Logf("res2 Process start sIIR: %v", res2.GetDebugProcessCallSIIR())
	t.Logf("res1 Process start delayBuf: %v", res1.GetDebugDelayBufFirst8())
	t.Logf("res2 Process start delayBuf: %v", res2.GetDebugDelayBufFirst8())

	t.Logf("\nOutput comparison:")
	t.Logf("res1 output (first 10): [%.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f]",
		out1[0], out1[1], out1[2], out1[3], out1[4], out1[5], out1[6], out1[7], out1[8], out1[9])
	t.Logf("res2 output (first 10): [%.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f, %.6f]",
		out2[0], out2[1], out2[2], out2[3], out2[4], out2[5], out2[6], out2[7], out2[8], out2[9])

	// Count differences
	diffCount := 0
	for i := 0; i < len(out1) && i < len(out2); i++ {
		if out1[i] != out2[i] {
			diffCount++
			if diffCount <= 10 {
				t.Logf("  Diff at [%d]: %.6f vs %.6f", i, out1[i], out2[i])
			}
		}
	}
	t.Logf("\nTotal differences: %d out of %d samples", diffCount, len(out1))

	if diffCount == 0 {
		t.Error("UNEXPECTED: outputs are identical despite different states!")
	} else {
		t.Log("EXPECTED: outputs differ due to different states")
	}
}
