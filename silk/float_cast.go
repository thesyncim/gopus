package silk

import "math"

// noFMA32 returns a*b as a float32, with guaranteed intermediate rounding.
// Go's compiler may fuse a multiply followed by an add into a single FMA
// instruction (FMADDS on ARM64), which performs only one rounding instead of
// two. When matching C code that uses separate FMUL + FADD instructions,
// the single-rounding FMA can differ by up to 1 ULP. Marking this function
// as go:noinline ensures the multiply result is materialized as a float32
// before the caller adds to it.
//
//go:noinline
func noFMA32(a, b float32) float32 {
	return a * b
}

// noFMA64 returns a*b as a float64, forcing the product to materialize before
// the caller adds or subtracts it. This mirrors the noFMA32 use when we need
// to prevent the compiler from contracting a multiply-add into a single FMA.
//
//go:noinline
func noFMA64(a, b float64) float64 {
	return a * b
}

func float64ToInt32Round(x float64) int32 {
	if x > float64(math.MaxInt32) {
		return math.MaxInt32
	}
	if x < float64(math.MinInt32) {
		return math.MinInt32
	}
	return int32(math.RoundToEven(x))
}

// float32ToInt32RoundEven mirrors lrintf-style round-to-nearest-even for
// small SILK Q-domain controls that are already in silk_float precision.
func float32ToInt32RoundEven(x float32) int32 {
	if x >= float32(math.MaxInt32) {
		return math.MaxInt32
	}
	if x <= float32(math.MinInt32) {
		return math.MinInt32
	}

	truncated := int32(x)
	frac := x - float32(truncated)
	if frac > 0.5 || (frac == 0.5 && truncated&1 != 0) {
		return truncated + 1
	}
	if frac < -0.5 || (frac == -0.5 && truncated&1 != 0) {
		return truncated - 1
	}
	return truncated
}
