// Package cgo compares stereo prediction delta calculation
package cgo

import (
	"testing"
)

const stereoInterpLenMsLocal = 8

func silkRSHIFT_ROUND_local(a int32, shift int) int32 {
	if shift <= 0 {
		return a
	}
	if shift == 1 {
		return (a >> 1) + (a & 1)
	}
	return ((a >> (shift - 1)) + 1) >> 1
}

func silkSMULBB_local(a, b int32) int32 {
	return int32(int16(a)) * int32(int16(b))
}

func computeDeltaGopus(predQ13, prevQ13, fsKHz int32) int32 {
	denomQ16 := int32((1 << 16) / (stereoInterpLenMsLocal * int(fsKHz)))
	return silkRSHIFT_ROUND_local(silkSMULBB_local(predQ13-prevQ13, denomQ16), 16)
}

func TestDeltaComparison(t *testing.T) {
	// Test with various predQ13 and prevQ13 values
	testCases := []struct {
		predQ13, prevQ13, fsKHz int32
	}{
		{5000, 0, 16},       // Typical case
		{-5000, 0, 16},      // Negative
		{13732, -13732, 16}, // Maximum range
		{0, 5450, 16},       // Packet 14's pred0=0, prev might be 5450
		{5450, 0, 16},       // Packet 14's pred1=5450
		{5892, 0, 16},       // From packet 13
		{-2737, 0, 16},      // From packet 13
		// Test with different sample rates
		{5000, 0, 8},
		{5000, 0, 12},
		{5000, 0, 48},
	}

	for i, tc := range testCases {
		goResult := computeDeltaGopus(tc.predQ13, tc.prevQ13, tc.fsKHz)
		libResult := LibopusComputeDelta(tc.predQ13, tc.prevQ13, tc.fsKHz)

		goDenom := int32((1 << 16) / (stereoInterpLenMsLocal * int(tc.fsKHz)))
		libDenom := LibopusGetDenomQ16(tc.fsKHz)

		if goResult != libResult || goDenom != libDenom {
			t.Logf("Test %d: pred=%d, prev=%d, fsKHz=%d", i, tc.predQ13, tc.prevQ13, tc.fsKHz)
			t.Logf("  goDenom=%d, libDenom=%d", goDenom, libDenom)
			t.Logf("  gopus delta=%d, libopus delta=%d, diff=%d", goResult, libResult, goResult-libResult)
		}
	}

	// Test the interpolation over the full range
	t.Logf("\nInterpolation test (fsKHz=16, 128 samples):")
	predQ13 := int32(5000)
	prevQ13 := int32(0)
	fsKHz := int32(16)

	goDelta := computeDeltaGopus(predQ13, prevQ13, fsKHz)
	libDelta := LibopusComputeDelta(predQ13, prevQ13, fsKHz)

	t.Logf("goDelta=%d, libDelta=%d", goDelta, libDelta)

	// Simulate the interpolation
	goPred := prevQ13
	libPred := prevQ13
	interpSamples := stereoInterpLenMsLocal * int(fsKHz) // 128

	t.Logf("After interpolation:")
	for n := 0; n < interpSamples; n++ {
		goPred += goDelta
		libPred += libDelta
	}
	t.Logf("  goPred final=%d, expected=%d, error=%d", goPred, predQ13, goPred-predQ13)
	t.Logf("  libPred final=%d, expected=%d, error=%d", libPred, predQ13, libPred-predQ13)
}

func TestDenomQ16Calculation(t *testing.T) {
	// Specifically test the denomQ16 calculation
	sampleRates := []int32{8, 12, 16, 24, 48}

	for _, fsKHz := range sampleRates {
		goDenom := int32((1 << 16) / (stereoInterpLenMsLocal * int(fsKHz)))
		libDenom := LibopusGetDenomQ16(fsKHz)

		if goDenom != libDenom {
			t.Errorf("fsKHz=%d: goDenom=%d, libDenom=%d, diff=%d", fsKHz, goDenom, libDenom, goDenom-libDenom)
		} else {
			t.Logf("fsKHz=%d: denom=%d (match)", fsKHz, goDenom)
		}
	}
}
