// Package cgo provides CGO comparison tests for SILK decoding.
package cgo

import (
	"math/bits"
	"testing"
)

// silkSMLABBGo is the Go implementation of SMLABB.
func silkSMLABBGo(a, b, c int32) int32 {
	return a + int32(int16(b))*int32(int16(c))
}

// silkSMULBBGo is the Go implementation of SMULBB.
func silkSMULBBGo(a, b int32) int32 {
	return int32(int16(a)) * int32(int16(b))
}

// silkSMULWBGo is the Go implementation of SMULWB.
func silkSMULWBGo(a, b int32) int32 {
	return int32((int64(a) * int64(int16(b))) >> 16)
}

// silkSMLAWBGo is the Go implementation of SMLAWB.
func silkSMLAWBGo(a, b, c int32) int32 {
	return a + int32((int64(b)*int64(int16(c)))>>16)
}

// TestSilkArithmeticFunctions tests if Go arithmetic matches libopus.
func TestSilkArithmeticFunctions(t *testing.T) {
	// Test cases with various values including edge cases
	testCases := []struct {
		a, b, c int32
	}{
		{0, 10000, 10000},
		{1000000, 30000, 30000},
		{-1000000, -30000, 30000},
		{1000000000, 30000, 30000},   // May overflow in SMLABB
		{-1000000000, -30000, 30000}, // May overflow in SMLABB
		{2000000000, 20000, 20000},   // Will overflow in SMLABB
		{-2000000000, 20000, 20000},  // Will overflow in SMLABB
		{0, 32767, 32767},            // Max int16 * max int16
		{0, -32768, -32768},          // Min int16 * min int16
		{2147483647, 1, 1},           // Max int32 + 1
	}

	t.Log("Testing SMULBB:")
	smulbbMismatches := 0
	for _, tc := range testCases {
		libResult := TestSilkSMULBB(tc.b, tc.c)
		goResult := silkSMULBBGo(tc.b, tc.c)
		if libResult != goResult {
			t.Logf("  MISMATCH: b=%d, c=%d: lib=%d, go=%d", tc.b, tc.c, libResult, goResult)
			smulbbMismatches++
		}
	}
	if smulbbMismatches == 0 {
		t.Log("  All SMULBB tests pass")
	}

	t.Log("Testing SMLABB vs SMLABB_ovflw:")
	smlabbMismatches := 0
	for _, tc := range testCases {
		libResult := TestSilkSMLABB(tc.a, tc.b, tc.c)
		libResultOvflw := TestSilkSMLABBOvflw(tc.a, tc.b, tc.c)
		goResult := silkSMLABBGo(tc.a, tc.b, tc.c)

		if libResult != libResultOvflw {
			t.Logf("  lib vs lib_ovflw differ: a=%d, b=%d, c=%d: lib=%d, lib_ovflw=%d",
				tc.a, tc.b, tc.c, libResult, libResultOvflw)
		}

		if goResult != libResultOvflw {
			t.Logf("  MISMATCH: a=%d, b=%d, c=%d: lib_ovflw=%d, go=%d",
				tc.a, tc.b, tc.c, libResultOvflw, goResult)
			smlabbMismatches++
		}
	}
	if smlabbMismatches == 0 {
		t.Log("  All SMLABB tests pass")
	}

	t.Log("Testing SMULWB:")
	smulwbMismatches := 0
	wbCases := []struct {
		a, b int32
	}{
		{0, 10000},
		{1000000, 10000},
		{-1000000, -10000},
		{2147483647, 32767},
		{-2147483648, 32767},
		{-2147483648, 1 << 14}, // Q31 * Q15 (min int32)
	}
	for _, tc := range wbCases {
		libResult := TestSilkSMULWB(tc.a, tc.b)
		goResult := silkSMULWBGo(tc.a, tc.b)
		if libResult != goResult {
			t.Logf("  MISMATCH: a=%d, b=%d: lib=%d, go=%d", tc.a, tc.b, libResult, goResult)
			smulwbMismatches++
		}
	}
	if smulwbMismatches == 0 {
		t.Log("  All SMULWB tests pass")
	}

	t.Log("Testing SMLAWB:")
	smlawbMismatches := 0
	for _, tc := range testCases {
		libResult := TestSilkSMLAWB(tc.a, tc.b, tc.c)
		goResult := silkSMLAWBGo(tc.a, tc.b, tc.c)
		if libResult != goResult {
			t.Logf("  MISMATCH: a=%d, b=%d, c=%d: lib=%d, go=%d",
				tc.a, tc.b, tc.c, libResult, goResult)
			smlawbMismatches++
		}
	}
	if smlawbMismatches == 0 {
		t.Log("  All SMLAWB tests pass")
	}

	totalMismatches := smulbbMismatches + smlabbMismatches + smulwbMismatches + smlawbMismatches
	if totalMismatches > 0 {
		t.Errorf("Found %d total arithmetic mismatches", totalMismatches)
	}
}

// TestSilkLPCAnalysisFilter tests the LPC analysis filter.
func TestSilkLPCAnalysisFilter(t *testing.T) {
	order := 10
	length := 100

	// Input signal (synthetic)
	in := make([]int16, length)
	for i := 0; i < length; i++ {
		in[i] = int16(1000 * ((i % 7) - 3))
	}

	// LPC coefficients (Q12)
	B := make([]int16, order)
	B[0] = 2048 // 0.5 in Q12
	B[1] = 1024 // 0.25 in Q12
	B[2] = -512 // -0.125 in Q12
	B[3] = 256
	B[4] = 128
	B[5] = 64
	B[6] = 32
	B[7] = 16
	B[8] = 8
	B[9] = 4

	// Libopus output
	libOut := SilkLPCAnalysisFilter(in, B, length, order)

	// Gopus output
	goOut := make([]int16, length)
	silkLPCAnalysisFilterGo(goOut, in, B, length, order)

	t.Logf("Comparing LPC analysis filter (len=%d, order=%d):", length, order)

	mismatches := 0
	for i := 0; i < length; i++ {
		if goOut[i] != libOut[i] {
			if mismatches < 20 {
				t.Logf("  [%2d] go=%6d lib=%6d diff=%d", i, goOut[i], libOut[i], goOut[i]-libOut[i])
			}
			mismatches++
		}
	}

	if mismatches > 0 {
		t.Errorf("Found %d mismatches out of %d samples", mismatches, length)
	} else {
		t.Log("  All outputs match!")
	}
}

// silkLPCAnalysisFilterGo is the Go implementation matching gopus.
func silkLPCAnalysisFilterGo(out []int16, in []int16, B []int16, length int, order int) {
	for i := 0; i < order; i++ {
		out[i] = 0
	}
	for ix := order; ix < length; ix++ {
		outQ12 := silkSMULBBGo(int32(in[ix-1]), int32(B[0]))
		for j := 1; j < order; j++ {
			outQ12 = silkSMLABBGo(outQ12, int32(in[ix-1-j]), int32(B[j]))
		}
		outQ12 = (int32(in[ix]) << 12) - outQ12
		out32 := (outQ12 + (1 << 11)) >> 12
		// Saturate to int16
		if out32 > 32767 {
			out32 = 32767
		} else if out32 < -32768 {
			out32 = -32768
		}
		out[ix] = int16(out32)
	}
}

// TestSilkDivisionFunctions tests DIV32_varQ and INVERSE32_varQ functions.
func TestSilkDivisionFunctions(t *testing.T) {
	// Test cases including values from actual decode
	divCases := []struct {
		a, b int32
		q    int
	}{
		{1 << 16, 1 << 16, 16},
		{1000000, 500000, 16},
		{-1000000, 500000, 16},
		{1000000, -500000, 16},
		// Values observed in decode
		{1 << 16, 63079296, 16}, // gainAdjQ16 computation
	}

	t.Log("Testing silk_DIV32_varQ:")
	for _, tc := range divCases {
		goResult := silkDiv32VarQGo(tc.a, tc.b, tc.q)
		libResult := TestSilkDIV32VarQ(tc.a, tc.b, tc.q)
		if goResult != libResult {
			t.Errorf("  DIV32_varQ(%d, %d, %d): go=%d, lib=%d", tc.a, tc.b, tc.q, goResult, libResult)
		}
	}
	t.Log("  All DIV32_varQ tests pass")

	// Test INVERSE32_varQ
	invCases := []struct {
		b int32
		q int
	}{
		{1 << 16, 47},
		{500000, 47},
		{63079296, 47}, // Observed gain value
		{61603840, 47}, // GainsQ16 = GainQ10 << 6 = 962560 << 6 from trace
		{1 << 20, 47},
	}

	t.Log("Testing silk_INVERSE32_varQ:")
	for _, tc := range invCases {
		goResult := silkInverse32VarQGo(tc.b, tc.q)
		libResult := TestSilkINVERSE32VarQ(tc.b, tc.q)
		t.Logf("  INVERSE32_varQ(%d, %d): go=%d, lib=%d", tc.b, tc.q, goResult, libResult)
		if goResult != libResult {
			t.Errorf("    MISMATCH!")
		}
	}
	t.Log("  All INVERSE32_varQ tests pass")

	// Test SMULWW
	t.Log("Testing silk_SMULWW:")
	smulwwCases := []struct {
		a, b int32
	}{
		{1000000, 1000000},
		{-1000000, 1000000},
		{2147483647, 32767},
		{-2147483648, 32767},
		{962560, 3732}, // Values from divergence point
	}
	for _, tc := range smulwwCases {
		goResult := silkSMULWWDiv(tc.a, tc.b)
		libResult := TestSilkSMULWW(tc.a, tc.b)
		if goResult != libResult {
			t.Errorf("  SMULWW(%d, %d): go=%d, lib=%d", tc.a, tc.b, goResult, libResult)
		}
	}
	t.Log("  All SMULWW tests pass")
}

// Go implementations for comparison
func silkDiv32VarQGo(a32, b32 int32, q int) int32 {
	if b32 == 0 {
		return 0
	}
	if q < 0 {
		q = 0
	}

	aHeadrm := int32(silkCLZ32Div(silkAbs32Div(a32)) - 1)
	a32Nrm := a32 << aHeadrm
	bHeadrm := int32(silkCLZ32Div(silkAbs32Div(b32)) - 1)
	b32Nrm := b32 << bHeadrm

	b32Inv := int32(0x7fffffff>>2) / int32(b32Nrm>>16)
	result := silkSMULWBGo(a32Nrm, b32Inv)

	a32Nrm = a32Nrm - (silkSMMULDiv(b32Nrm, result) << 3)
	result = silkSMLAWBGo(result, a32Nrm, b32Inv)

	lshift := int(29 + aHeadrm - bHeadrm - int32(q))
	if lshift < 0 {
		return silkLShiftSAT32Div(result, -lshift)
	}
	if lshift < 32 {
		return result >> lshift
	}
	return 0
}

func silkInverse32VarQGo(b32 int32, q int) int32 {
	if b32 == 0 || q <= 0 {
		return 0
	}

	bHeadrm := int32(silkCLZ32Div(silkAbs32Div(b32)) - 1)
	b32Nrm := b32 << bHeadrm

	b32Inv := int32(0x7fffffff>>2) / int32(b32Nrm>>16)
	result := int32(b32Inv) << 16

	errQ32 := int32((1<<29)-silkSMULWBGo(b32Nrm, b32Inv)) << 3
	result = silkSMLAWWDiv(result, errQ32, b32Inv)

	lshift := int(61 - bHeadrm - int32(q))
	if lshift <= 0 {
		return silkLShiftSAT32Div(result, -lshift)
	}
	if lshift < 32 {
		return result >> lshift
	}
	return 0
}

func silkCLZ32Div(x int32) int32 {
	return int32(bits.LeadingZeros32(uint32(x)))
}

func silkAbs32Div(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func silkSMULWWDiv(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 16)
}

func silkSMLAWWDiv(a, b, c int32) int32 {
	return a + int32((int64(b)*int64(c))>>16)
}

func silkSMMULDiv(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 32)
}

func silkLShiftSAT32Div(x int32, shift int) int32 {
	v := int64(x) << shift
	if v > int64((1<<31)-1) {
		return int32((1 << 31) - 1)
	}
	if v < int64(-1<<31) {
		return int32(-1 << 31)
	}
	return int32(v)
}
