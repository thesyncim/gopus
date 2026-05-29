//go:build gopus_fixedpoint

package fixedpoint

// This file ports the integer building blocks of the libopus FIXED_POINT
// CELT band-energy path (celt/bands.c compute_band_energies / normalise_bands):
//   - celt_rsqrt_norm32 (Q31 in, Q29 out)
//   - celt_sqrt32       (Qx in, Q(x/2+16) out)
//
// celt_sqrt32 is the kernel compute_band_energies uses to turn an accumulated
// Q31 energy into a band amplitude; celt_rsqrt_norm32 is its only new
// dependency. Both are bit-exact to celt/mathops.c on a fast-int64 host
// (OPUS_FAST_INT64 == 1, which holds for amd64 and arm64/LP64), where
// MULT32_32_Q31 is the 64-bit form.

// mult32x32q31 implements libopus MULT32_32_Q31(a,b) for the OPUS_FAST_INT64
// path: the full 64-bit product arithmetic-shifted right by 31, narrowed to
// int32.
func mult32x32q31(a, b int32) int32 {
	return int32((int64(a) * int64(b)) >> 31)
}

// sub32 subtracts two int32 values with natural wraparound, equivalent to
// libopus SUB32(a,b) in fixed-point builds.
func sub32(a, b int32) int32 {
	return a - b
}

// CeltRsqrtNorm32 computes the reciprocal square root of x in the range
// [0.25, 1). Input x is Q31, output is Q29.
// Matches libopus celt/mathops.c celt_rsqrt_norm32() (FIXED_POINT path).
func CeltRsqrtNorm32(x int32) int32 {
	// r_q29 = SHL32(celt_rsqrt_norm(SHR32(x, 31-16)), 15)
	// celt_rsqrt_norm takes Q16 in / Q14 out; left-shifting by 15 lifts Q14->Q29.
	rQ29 := int32(CeltRsqrtNorm(x>>(31-16))) << 15

	// Newton-Raphson refinement: r = r*(1.5 - 0.5*x*r*r)
	tmp := mult32x32q31(rQ29, rQ29)
	tmp = mult32x32q31(1073741824 /* Q31 */, tmp)
	tmp = mult32x32q31(x, tmp)
	return mult32x32q31(rQ29, sub32(201326592 /* Q27 */, tmp)) << 4
}

// CeltSqrt32 approximates sqrt(x). For a Qx input the output is in
// Q(x/2+16) format. x must satisfy 0 <= x; values >= 2^30 saturate to
// 2^31-1.
// Matches libopus celt/mathops.c celt_sqrt32() (FIXED_POINT path) bit-for-bit.
func CeltSqrt32(x int32) int32 {
	if x == 0 {
		return 0
	}
	if x >= 1073741824 {
		return 2147483647 // 2^31 - 1
	}
	// k = celt_ilog2(x)>>1
	k := int(CeltILog2(x) >> 1)
	// x_frac = VSHR32(x, 2*(k-14)-1)
	xFrac := vshr32(x, 2*(k-14)-1)
	// x_frac = MULT32_32_Q31(celt_rsqrt_norm32(x_frac), x_frac)
	xFrac = mult32x32q31(CeltRsqrtNorm32(xFrac), xFrac)
	if k < 12 {
		// PSHR32(x_frac, 12-k)
		return pshr32(xFrac, 12-k)
	}
	// SHL32(x_frac, k-12)
	return xFrac << (k - 12)
}

// pshr32 implements libopus PSHR32(a, shift): rounded arithmetic right shift,
// SHR32(a + (1<<shift>>1), shift). shift must be >= 0.
func pshr32(a int32, shift int) int32 {
	return (a + ((1 << shift) >> 1)) >> shift
}
