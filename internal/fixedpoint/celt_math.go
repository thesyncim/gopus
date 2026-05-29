// Package fixedpoint holds the first increment of gopus fixed-point work:
// pure-integer CELT math kernels ported from libopus celt/mathops.h
// (FIXED_POINT path).  These are self-contained polynomial approximations
// that operate entirely in the integer domain; no floating-point is used.
//
// Q-notation used here matches libopus:
//   Q14: 1.0 = 1<<14 = 16384
//   Q10: 1.0 = 1<<10 = 1024
//   Q15: 1.0 = 1<<15 = 32768
//   Q16: 1.0 = 1<<16 = 65536
package fixedpoint

import "math/bits"

// CeltILog2 returns floor(log2(x)) for x > 0.
// Equivalent to EC_ILOG(x)-1 in libopus.
func CeltILog2(x int32) int16 {
	return int16(31 - bits.LeadingZeros32(uint32(x)))
}

// CeltLog2 approximates log2(x) in fixed-point.
// Input x is Q14 (1.0 = 16384), must be > 0.
// Output is Q10 (1.0 = 1024).
// Matches libopus celt/mathops.h celt_log2() (FIXED_POINT path).
func CeltLog2(x int32) int16 {
	if x == 0 {
		return -32767
	}

	// C[5] = {-6801+(1<<(13-10)), 15746, -5217, 2545, -1401}
	// = {-6793, 15746, -5217, 2545, -1401}
	const (
		c0 int16 = -6793
		c1 int16 = 15746
		c2 int16 = -5217
		c3 int16 = 2545
		c4 int16 = -1401
	)

	i := CeltILog2(x)
	// n = VSHR32(x, i-15) - 32768 - 16384; result is int16
	var n int16
	shift := int(i) - 15
	var vshr int32
	if shift >= 0 {
		vshr = x >> shift
	} else {
		vshr = x << (-shift)
	}
	// Subtract 32768+16384 in int32 before narrowing to int16.
	// 32768 overflows int16, so the subtraction must happen in a wider type.
	n = int16(vshr - 32768 - 16384)

	// Horner evaluation of degree-4 polynomial:
	// frac = C[0] + n*(C[1] + n*(C[2] + n*(C[3] + n*C[4])))
	frac := mult16x16q15(n, c4)
	frac = add16s(c3, frac)
	frac = mult16x16q15(n, frac)
	frac = add16s(c2, frac)
	frac = mult16x16q15(n, frac)
	frac = add16s(c1, frac)
	frac = mult16x16q15(n, frac)
	frac = add16s(c0, frac)

	return int16((int32(i-13) << 10) + int32(frac>>4))
}

// CeltExp2Frac returns the fractional part of 2^x where x is in Q10.
// Only the fractional bits of x are used (x & 0x3FF in Q10 == x mod 1.0).
// Output is approximately Q14 (range ≈ [1.0, 2.0)).
// Matches libopus celt/mathops.h celt_exp2_frac() (FIXED_POINT path).
//
// Constants:
//
//	D0=16383, D1=22804, D2=14819, D3=10204
func CeltExp2Frac(x int16) int16 {
	const (
		d0 int16 = 16383
		d1 int16 = 22804
		d2 int16 = 14819
		d3 int16 = 10204
	)
	frac := int16(int32(x) << 4) // SHL16(x, 4): Q10 -> Q14
	// ADD16(D0, MULT16_16_Q15(frac, ADD16(D1, MULT16_16_Q15(frac, ADD16(D2, MULT16_16_Q15(D3, frac))))))
	inner := add16s(d2, mult16x16q15(d3, frac))
	inner = add16s(d1, mult16x16q15(frac, inner))
	inner = add16s(d0, mult16x16q15(frac, inner))
	return inner
}

// CeltExp2 computes 2^x in fixed-point.
// Input x is Q10 (1.0 = 1024).
// Output is Q16 (1.0 = 65536).
// Matches libopus celt/mathops.h celt_exp2() (FIXED_POINT path).
func CeltExp2(x int16) int32 {
	integer := int(x) >> 10 // SHR16(x, 10)
	if integer > 14 {
		return 0x7f000000
	}
	if integer < -15 {
		return 0
	}
	frac := CeltExp2Frac(x - int16(integer<<10)) // Q14
	// VSHR32(EXTEND32(frac), -integer-2)
	// When integer=0: shift = -0-2 = -2, so left-shift by 2: Q14 -> Q16
	shift := -integer - 2
	if shift >= 0 {
		return int32(frac) >> shift
	}
	return int32(frac) << (-shift)
}

// CeltRsqrtNorm computes 1/sqrt(x) for x in [0.25, 1.0) represented in Q16.
// Output is Q14.
// Matches libopus celt/mathops.c celt_rsqrt_norm() (FIXED_POINT path).
func CeltRsqrtNorm(x int32) int16 {
	// n = x - 32768; n is Q15-ish offset
	n := int16(x - 32768)

	// Initial guess: r = 23557 + n*(-13490 + n*6713) (all Q14)
	r := add16s(23557, mult16x16q15(n, add16s(-13490, mult16x16q15(n, 6713))))

	// r2 = r*r in Q15 -> Q14 (MULT16_16_Q15(r,r))
	r2 := mult16x16q15(r, r)

	// y = 2*(r2*n/32768 + r2 - 16384) = 2*(MULT16_16_Q15(r2,n) + r2 - 16384)
	y := int16(2 * (mult16x16q15(r2, n) + r2 - 16384))

	// r += r * y * (y*0.375 - 0.5)
	// = r + MULT16_16_Q15(r, MULT16_16_Q15(y, MULT16_16_Q15(y,12288) - 16384))
	correction := mult16x16q15(r, mult16x16q15(y, add16s(mult16x16q15(y, 12288), -16384)))
	return r + correction
}

// mult16x16q15 multiplies two int16 values and returns the result shifted right
// by 15, equivalent to libopus MULT16_16_Q15(a,b).
func mult16x16q15(a, b int16) int16 {
	return int16((int32(a) * int32(b)) >> 15)
}

// add16s adds two int16 values with natural int16 truncation (wrap on overflow),
// equivalent to libopus ADD16(a,b) in fixed-point builds.
func add16s(a, b int16) int16 {
	return a + b
}
