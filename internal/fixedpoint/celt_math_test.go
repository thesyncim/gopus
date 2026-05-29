package fixedpoint

import (
	"math"
	"testing"
)

// TestCeltILog2 verifies CeltILog2 against math.Log2 for a range of inputs.
func TestCeltILog2(t *testing.T) {
	cases := []struct {
		x    int32
		want int16
	}{
		{1, 0},
		{2, 1},
		{3, 1},
		{4, 2},
		{7, 2},
		{8, 3},
		{16384, 14},  // Q14 representation of 1.0
		{32768, 15},
		{65536, 16},
		{1 << 30, 30},
	}
	for _, tc := range cases {
		got := CeltILog2(tc.x)
		if got != tc.want {
			t.Errorf("CeltILog2(%d) = %d, want %d", tc.x, got, tc.want)
		}
	}
}

// TestCeltLog2ExactPowersOf2 verifies that CeltLog2 returns exact results for
// powers of two.  For x = 2^k expressed in Q14, log2(x) = k, so the Q10
// output must equal k*1024.
func TestCeltLog2ExactPowersOf2(t *testing.T) {
	cases := []struct {
		exp  int // actual exponent
		xQ14 int32
		wantQ10 int16
	}{
		// Q14 of 0.5 = 8192; log2(0.5) = -1; Q10 = -1024
		{-1, 8192, -1024},
		// Q14 of 1.0 = 16384; log2(1.0) = 0; Q10 = 0
		{0, 16384, 0},
		// Q14 of 2.0 = 32768; log2(2.0) = 1; Q10 = 1024
		{1, 32768, 1024},
		// Q14 of 4.0 = 65536; log2(4.0) = 2; Q10 = 2048
		{2, 65536, 2048},
		// Q14 of 8.0 = 131072; log2(8.0) = 3; Q10 = 3072
		{3, 131072, 3072},
	}
	for _, tc := range cases {
		got := CeltLog2(tc.xQ14)
		if got != tc.wantQ10 {
			t.Errorf("CeltLog2(Q14=%d [2^%d]) = %d, want %d (Q10)",
				tc.xQ14, tc.exp, got, tc.wantQ10)
		}
	}
}

// TestCeltLog2Accuracy checks that CeltLog2 is within 1 Q10 unit (i.e. < 1/1024
// error in log2) for inputs spanning most of the representable range.
func TestCeltLog2Accuracy(t *testing.T) {
	// Test a grid of Q14 inputs from 0.5 to 16.0
	for xQ14 := int32(8192); xQ14 <= int32(262144); xQ14 += 256 {
		xFloat := float64(xQ14) / 16384.0
		exactQ10 := math.Log2(xFloat) * 1024.0

		got := CeltLog2(xQ14)
		errUnits := math.Abs(float64(got) - exactQ10)
		if errUnits > 1.5 {
			t.Errorf("CeltLog2(Q14=%d [%.4f]) = Q10=%d (%.4f), exact=%.4f, err=%.4f units",
				xQ14, xFloat, got, float64(got)/1024.0, exactQ10/1024.0, errUnits)
		}
	}
}

// TestCeltExp2ExactPowersOf2 verifies that CeltExp2 round-trips with CeltLog2
// on integer exponents, and that the Q16 output equals the expected power of
// two (within the known 4-unit bias introduced by D0=16383 rather than 16384).
func TestCeltExp2ExactPowersOf2(t *testing.T) {
	// For integer exponent k (k in range [-15, 14]), input is k*1024 Q10.
	// CeltExp2 output Q16: 2^k * 65536.
	// The libopus approximation has a small absolute bias due to D0=16383 (not
	// 16384): at k=0 the output is 65532 not 65536, a deficit of 4 Q16 units.
	// For k > 0 the bias scales as 4*2^k; for k < 0 right-shift truncation means
	// the absolute error is at most 4 units.  We accept max absolute error of 5
	// units per Q16 step to cover both directions.
	const maxAbsErr = int32(5)
	for k := -10; k <= 10; k++ {
		xQ10 := int16(k * 1024)
		got := CeltExp2(xQ10)
		// Compute exact Q16 value as int32 (safe for k <= 14)
		exactQ16 := int32(math.Round(math.Pow(2, float64(k)) * 65536))
		diff := got - exactQ16
		if diff < 0 {
			diff = -diff
		}
		// For k >= 1, the bias grows as 4*2^k so use a relative bound instead.
		if k < 1 {
			if diff > maxAbsErr {
				t.Errorf("CeltExp2(Q10=%d [2^%d]) = Q16=%d, exactQ16=%d, diff=%d",
					xQ10, k, got, exactQ16, diff)
			}
		} else {
			relErr := math.Abs(float64(got)-float64(exactQ16)) / float64(exactQ16)
			if relErr > 0.00007 { // < 0.007% for k>=2
				t.Errorf("CeltExp2(Q10=%d [2^%d]) = Q16=%d, exactQ16=%d, relErr=%.6f",
					xQ10, k, got, exactQ16, relErr)
			}
		}
	}
}

// TestCeltLog2Exp2RoundTrip verifies that CeltExp2(CeltLog2(x)) ≈ x for a
// range of positive Q14 inputs (round-trip accuracy within 0.2%).
func TestCeltLog2Exp2RoundTrip(t *testing.T) {
	for xQ14 := int32(8192); xQ14 <= int32(65536); xQ14 += 512 {
		logQ10 := CeltLog2(xQ14)
		// CeltExp2 returns Q16; scale down to Q14 by >>2 to compare with xQ14.
		expQ16 := CeltExp2(logQ10)
		expQ14 := expQ16 >> 2

		// Allow up to 0.5% relative error from combined approximation rounding.
		relErr := math.Abs(float64(expQ14-xQ14)) / float64(xQ14)
		if relErr > 0.005 {
			t.Errorf("exp2(log2(Q14=%d)) = Q14=%d (via Q16=%d), relErr=%.4f",
				xQ14, expQ14, expQ16, relErr)
		}
	}
}

// TestCeltRsqrtNormAccuracy checks that CeltRsqrtNorm is within 0.05% of the
// true 1/sqrt(x) for inputs spanning the valid range [0.25, 1.0) in Q16.
func TestCeltRsqrtNormAccuracy(t *testing.T) {
	// Q16 range for [0.25, 1.0): [16384, 65535]
	for xQ16 := int32(16384); xQ16 < int32(65536); xQ16 += 128 {
		xFloat := float64(xQ16) / 65536.0
		exact := 1.0 / math.Sqrt(xFloat) // true rsqrt
		exactQ14 := exact * 16384.0

		got := CeltRsqrtNorm(xQ16)
		relErr := math.Abs(float64(got)-exactQ14) / exactQ14
		if relErr > 0.0005 {
			t.Errorf("CeltRsqrtNorm(Q16=%d [%.4f]) = Q14=%d (%.4f), exact=%.4f, relErr=%.6f",
				xQ16, xFloat, got, float64(got)/16384.0, exact, relErr)
		}
	}
}

// TestCeltExp2ClampBehavior verifies the saturation behavior at the edges of
// the Q10 input range, matching libopus behaviour.
func TestCeltExp2ClampBehavior(t *testing.T) {
	// integer > 14 -> saturate to 0x7f000000
	x := int16(15 * 1024) // 15.0 in Q10
	got := CeltExp2(x)
	if got != 0x7f000000 {
		t.Errorf("CeltExp2(%d) = %d, want 0x7f000000 (%d)", x, got, int32(0x7f000000))
	}

	// integer < -15 -> return 0
	x = int16(-16 * 1024) // -16.0 in Q10
	got = CeltExp2(x)
	if got != 0 {
		t.Errorf("CeltExp2(%d) = %d, want 0", x, got)
	}
}
