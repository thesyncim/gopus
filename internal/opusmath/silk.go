package opusmath

import "math"

// FMA32 is a correctly rounded float32 fused multiply-add: a*b+c with a single
// rounding to float32, the result of the FMADDS / VFMADD*PS instructions the
// libopus CELT AVX2 and arm64 NEON kernels use.
//
// The float32 product is exact in float64 (48 significant bits). The float64
// sum of the product and c can round once, so it is re-rounded to odd (TwoSum
// recovers the exact error; an inexact even result moves one float64 ulp toward
// the exact sum). Rounding a round-to-odd float64 to float32 then yields the
// correctly rounded float32 result, which float32(math.FMA(a, b, c)) does not
// guarantee: the double rounding of the float64 FMA result can land on a
// float32 tie (0x3fcca800*0x3f979800 + 0xa20c2545 is 0x3ff26137, not
// 0x3ff26138). The float64 values are transient and never stored as codec
// state or scratch.
func FMA32(a, b, c float32) float32 {
	p := float64(a) * float64(b)
	cc := float64(c)
	s := p + cc
	if s != s {
		// NaN results keep the operand-order NaN propagation of the hardware
		// FMA, which math.FMA reproduces.
		return float32(math.FMA(float64(a), float64(b), cc))
	}
	v := s - p
	e := (p - (s - v)) + (cc - v)
	if e != 0 && !math.IsInf(s, 0) {
		bits := math.Float64bits(s)
		if bits&1 == 0 {
			if (e > 0) == (s > 0) {
				bits++
			} else {
				bits--
			}
			s = math.Float64frombits(bits)
		}
	}
	return float32(s)
}

// CReal is the C double type as used by the libopus floating-point (FLP) build.
// It is an alias (not a defined type) so values flow to and from float64 helpers
// without conversions, exactly as C double does.
//
// libopus' FLP code is mostly single precision, but a handful of accumulators
// are intentionally declared double even there to keep enough precision for
// bit-exactness against the reference. Marking those operands CReal documents
// that the double width is deliberate and stops them from being narrowed to
// float32. Examples include silk_energy_FLP(), silk_inner_product_FLP_c(),
// silk_burg_modified_FLP(), silk_schur_FLP(), warped_autocorrelation_FLP() and
// corrMatrix_FLP() (all in silk/float/).
type CReal = float64

// RoundF32HalfAwayFromZeroToInt32 rounds x to the nearest integer with ties
// going away from zero. It matches libopus silk_float2int() (silk/float/SigProc_FLP.h),
// i.e. (opus_int32)floor(x + 0.5) for non-negative x and the symmetric
// ceil(x - 0.5) for negatives. The +/-0.5 bias and the floor/ceil run in double
// so the rounding boundary is exact before the int32 truncation.
func RoundF32HalfAwayFromZeroToInt32(x float32) int32 {
	if x >= 0 {
		return int32(math.Floor(float64(x) + 0.5))
	}
	return int32(math.Ceil(float64(x) - 0.5))
}

// FloorF32ToInt32 truncates toward negative infinity, the C (opus_int32)floor(x)
// idiom used across the SILK FLP code. The floor is taken in double before
// narrowing so the result is bit-exact with libopus.
func FloorF32ToInt32(x float32) int32 {
	return int32(math.Floor(float64(x)))
}

// ExpF32 mirrors the C exp() used in the SILK FLP paths (e**x). libopus computes
// it in double and narrows once on assignment to a float; doing the math in
// float64 and converting the single final result reproduces that rounding.
func ExpF32(x float32) float32 {
	return float32(math.Exp(float64(x)))
}

// Exp2F32 mirrors C exp2() narrowed to float (2**x), rounded to float exactly
// once, as in libopus.
func Exp2F32(x float32) float32 {
	return float32(math.Exp2(float64(x)))
}

// Pow10F32 mirrors C pow(10, x) narrowed to float, the 10**x form libopus uses
// for decibel conversions in the FLP gain code.
func Pow10F32(x float32) float32 {
	return float32(math.Pow(10, float64(x)))
}

// SinF32 mirrors C sin() narrowed to float.
func SinF32(x float32) float32 {
	return float32(math.Sin(float64(x)))
}

// CosF32 mirrors C cos() narrowed to float.
func CosF32(x float32) float32 {
	return float32(math.Cos(float64(x)))
}

// CELTCosNormF32 matches libopus celt/mathops.h celt_cos_norm() in the
// floating-point build: (float)cos((.5f*PI)*x). CELT exp_rotation() in
// celt/vq.c relies on that C double cos() before narrowing back to float, so the
// half-pi product is formed and evaluated in double here too.
func CELTCosNormF32(x float32) float32 {
	const halfPi = 0.5 * 3.1415926535897931
	return float32(math.Cos(halfPi * float64(x)))
}

// AcosF32 mirrors C acos() narrowed to float.
func AcosF32(x float32) float32 {
	return float32(math.Acos(float64(x)))
}

// Exp2DivIntF32 returns (2**x)/denom as a float, matching the libopus FLP
// expression exp2(x)/N. The division is carried out in double (denom widened to
// float64) before the single narrowing to float, so the quotient rounds exactly
// as the C code does.
func Exp2DivIntF32(x float32, denom int) float32 {
	return float32(math.Exp2(float64(x)) / float64(denom))
}

// Log2F32 mirrors C log2() narrowed to float.
func Log2F32(x float32) float32 {
	return float32(math.Log2(float64(x)))
}

// LogF32 mirrors C log() (natural log) narrowed to float.
func LogF32(x float32) float32 {
	return float32(math.Log(float64(x)))
}

// Log10F32 mirrors C log10() narrowed to float.
func Log10F32(x float32) float32 {
	return float32(math.Log10(float64(x)))
}

// ExpCReal is the double-precision e**x for the FLP helpers that keep their
// accumulator in C double (see [CReal]); no narrowing to float occurs.
func ExpCReal(x CReal) CReal {
	return CReal(math.Exp(float64(x)))
}

// LogCReal is the double-precision natural log for the C-double FLP helpers.
func LogCReal(x CReal) CReal {
	return CReal(math.Log(float64(x)))
}

// Log10CReal is the double-precision log10 for the C-double FLP helpers.
func Log10CReal(x CReal) CReal {
	return CReal(math.Log10(float64(x)))
}

// PowCReal is the double-precision x**y for the C-double FLP helpers.
func PowCReal(x, y CReal) CReal {
	return CReal(math.Pow(float64(x), float64(y)))
}

// SilkLog2F32 matches silk/float/SigProc_FLP.h silk_log2(): log2(x) computed as
// 3.32192809488736 * log10(x). libopus uses exactly this log10-based identity
// (not a native log2), and the constant 1/log10(2) is the literal from the
// header, so reproducing the same expression is required for bit-exactness.
func SilkLog2F32(x float32) float32 {
	return float32(3.32192809488736 * math.Log10(float64(x)))
}

// SqrtF32 mirrors C sqrtf()/sqrt() narrowed to float.
func SqrtF32(x float32) float32 {
	return float32(math.Sqrt(float64(x)))
}

// SqrtCReal is the double-precision sqrt for the C-double FLP helpers.
func SqrtCReal(x CReal) CReal {
	return CReal(math.Sqrt(float64(x)))
}

// FloorHalfPlusF32ToInt32 returns floor(x + 0.5) as int32, the plain
// round-half-up form (toward +inf) libopus uses where ties on negatives are not
// special-cased; contrast with [RoundF32HalfAwayFromZeroToInt32]. The bias and
// floor are taken in double before narrowing.
func FloorHalfPlusF32ToInt32(x float32) int32 {
	return int32(math.Floor(float64(x) + 0.5))
}

// FloorCRealToInt32 truncates a C-double value toward negative infinity to int32.
func FloorCRealToInt32(x CReal) int32 {
	return int32(math.Floor(float64(x)))
}

// RoundToEvenF32ToInt32 rounds to the nearest integer with ties to even
// (banker's rounding), matching the C rint()/nearbyint() default rounding mode
// libopus assumes where round-to-nearest-even is intended.
func RoundToEvenF32ToInt32(x float32) int32 {
	return int32(math.RoundToEven(float64(x)))
}
