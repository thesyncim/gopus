// Package cgo compares SMLAWB implementations
package cgo

import (
	"testing"
)

func silkSMLAWB_gopus(a, b, c int32) int32 {
	return a + int32((int64(b)*int64(int16(c)))>>16)
}

func silkSMLAWB_libopusGo(a, b, c int32) int32 {
	// Exact translation of libopus macro
	high := (b >> 16) * int32(int16(c))
	low := ((b & 0x0000FFFF) * int32(int16(c))) >> 16
	return a + high + low
}

func TestSMLAWBComparison(t *testing.T) {
	// Test cases with various values
	testCases := []struct {
		a, b, c int32
	}{
		{0, 0x12345678, 0x7FFF},
		{0, 0x7FFFFFFF, 0x7FFF},
		{0, -2147483647, 0x7FFF},
		{0, 0x12345678, -1},
		{0, 0x12345678, 0x4000},
		{1000, 256, 8192},
		{0, 1 << 20, 1 << 13}, // Values like in stereo pred
		{0, -(1 << 20), 1 << 13},
		// Realistic stereo prediction values
		{0, 1024, 8192},    // pred around Q13
		{65536, 8192, 512}, // typical Q16/Q13 values
	}

	for i, tc := range testCases {
		goResult := silkSMLAWB_gopus(tc.a, tc.b, tc.c)
		libResult := LibopusSMLAWB(tc.a, tc.b, tc.c)
		goLibopus := silkSMLAWB_libopusGo(tc.a, tc.b, tc.c)

		if goResult != libResult || goResult != goLibopus {
			t.Logf("Test %d: a=%d, b=%d (0x%08X), c=%d", i, tc.a, tc.b, uint32(tc.b), tc.c)
			t.Logf("  gopus:        %d", goResult)
			t.Logf("  libopus C:    %d", libResult)
			t.Logf("  libopus Go:   %d", goLibopus)
			t.Logf("  diff gopus-C: %d", goResult-libResult)
		}
	}

	// Random test
	mismatches := 0
	for b := int32(-100000); b < 100000; b += 1000 {
		for c := int32(-10000); c < 10000; c += 500 {
			goResult := silkSMLAWB_gopus(0, b, c)
			libResult := LibopusSMLAWB(0, b, c)
			if goResult != libResult {
				mismatches++
				if mismatches <= 5 {
					t.Logf("Mismatch: b=%d, c=%d, gopus=%d, libopus=%d, diff=%d",
						b, c, goResult, libResult, goResult-libResult)
				}
			}
		}
	}

	if mismatches > 0 {
		t.Logf("Total mismatches: %d", mismatches)
	} else {
		t.Logf("All %d tests passed", 200*40)
	}
}

func TestSMLAWBEdgeCases(t *testing.T) {
	// Test with values that trigger the difference
	// The issue is when (b & 0xFFFF) * c16 is negative and shifts right

	// Case: b has low bits set, c is negative
	b := int32(0x00010001) // low 16 bits = 1, high 16 bits = 1
	c := int32(-1)         // c16 = -1

	goResult := silkSMLAWB_gopus(0, b, c)
	libResult := LibopusSMLAWB(0, b, c)

	t.Logf("b=0x%08X, c=%d", uint32(b), c)
	t.Logf("gopus:  %d", goResult)
	t.Logf("libopus: %d", libResult)

	// Break down libopus calculation
	high := (b >> 16) * int32(int16(c))
	lowProd := (b & 0x0000FFFF) * int32(int16(c))
	low := lowProd >> 16

	t.Logf("high=%d, lowProd=%d, low=%d", high, lowProd, low)
}

func silkSMULWB_gopus(a, b int32) int32 {
	return int32((int64(a) * int64(int16(b))) >> 16)
}

func silkSMLABB_gopus(a, b, c int32) int32 {
	return a + int32(int16(b))*int32(int16(c))
}

func TestSMULWBComparison(t *testing.T) {
	testCases := []struct {
		a, b int32
	}{
		{0x12345678, 0x7FFF},
		{0x7FFFFFFF, 0x7FFF},
		{0x12345678, -1},
		{0x12345678, 0x4000},
		{1 << 20, 1 << 13},
		{-(1 << 20), 1 << 13},
		{1024, 8192},
		{8192, 512},
		// Values from stereo prediction
		{-13732, 6554}, // Q13 table value * step
		{10050, 6554},
	}

	mismatches := 0
	for i, tc := range testCases {
		goResult := silkSMULWB_gopus(tc.a, tc.b)
		libResult := LibopusSMULWB(tc.a, tc.b)

		if goResult != libResult {
			mismatches++
			t.Logf("Test %d: a=%d (0x%08X), b=%d", i, tc.a, uint32(tc.a), tc.b)
			t.Logf("  gopus:  %d", goResult)
			t.Logf("  libopus: %d", libResult)
			t.Logf("  diff: %d", goResult-libResult)
		}
	}

	if mismatches > 0 {
		t.Errorf("%d mismatches found in SMULWB", mismatches)
	} else {
		t.Logf("All SMULWB tests passed")
	}
}

func TestSMLABBComparison(t *testing.T) {
	testCases := []struct {
		a, b, c int32
	}{
		{0, 100, 200},
		{1000, -100, 200},
		{0, 0x7FFF, 0x7FFF},
		{0, -32768, 32767},
		// Values from stereo prediction
		{-13732, 6554, 5}, // lowQ13, stepQ13, 2*ix[i][1]+1
		{10050, -1000, 3},
	}

	mismatches := 0
	for i, tc := range testCases {
		goResult := silkSMLABB_gopus(tc.a, tc.b, tc.c)
		libResult := LibopusSMLABB(tc.a, tc.b, tc.c)

		if goResult != libResult {
			mismatches++
			t.Logf("Test %d: a=%d, b=%d, c=%d", i, tc.a, tc.b, tc.c)
			t.Logf("  gopus:  %d", goResult)
			t.Logf("  libopus: %d", libResult)
			t.Logf("  diff: %d", goResult-libResult)
		}
	}

	if mismatches > 0 {
		t.Errorf("%d mismatches found in SMLABB", mismatches)
	} else {
		t.Logf("All SMLABB tests passed")
	}
}
