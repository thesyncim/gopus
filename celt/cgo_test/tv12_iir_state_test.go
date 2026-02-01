//go:build cgo_libopus
// +build cgo_libopus

// Package cgo tests IIR state impact on resampler output.
package cgo

import (
	"testing"

	"github.com/thesyncim/gopus/silk"
)

// TestIIRStateImpact verifies that IIR state affects resampler output.
func TestIIRStateImpact(t *testing.T) {
	// Create two resamplers for NB->48kHz
	res1 := silk.NewLibopusResampler(8000, 48000)
	res2 := silk.NewLibopusResampler(8000, 48000)

	// Create identical input
	input := make([]float32, 160)
	for i := range input {
		input[i] = float32(i) / 32768.0
	}

	// Process with res1 (zeroed state)
	out1 := res1.Process(input)
	t.Logf("Res1 (zero state) output[0:5] = [%.6f, %.6f, %.6f, %.6f, %.6f]",
		out1[0], out1[1], out1[2], out1[3], out1[4])
	t.Logf("Res1 state after: sIIR = %v", res1.GetSIIR())

	// Manually set res2 to have non-zero state
	res2.SetSIIR([6]int32{-630071, -83139, -749660, -440000, -330000, -220000})
	t.Logf("Res2 initial state: sIIR = %v", res2.GetSIIR())

	// Process with res2 (non-zero state)
	out2 := res2.Process(input)
	t.Logf("Res2 (non-zero state) output[0:5] = [%.6f, %.6f, %.6f, %.6f, %.6f]",
		out2[0], out2[1], out2[2], out2[3], out2[4])
	t.Logf("Res2 state after: sIIR = %v", res2.GetSIIR())

	// Compare outputs - check multiple positions since early samples come from delay buffer
	anyDiff := false
	for i := 0; i < 10 && i < len(out1) && i < len(out2); i++ {
		if out1[i] != out2[i] {
			t.Logf("EXPECTED: outputs differ at [%d]: %.6f vs %.6f", i, out1[i], out2[i])
			anyDiff = true
		}
	}

	if !anyDiff {
		t.Error("UNEXPECTED: first 10 outputs are identical despite different initial states!")
		t.Log("This means either Reset doesn't work or IIR state isn't being used")
	} else {
		t.Log("IIR state DOES affect output - the resampler is working correctly")
	}
}
