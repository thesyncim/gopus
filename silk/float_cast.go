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

func float64ToInt32Round(x float64) int32 {
	if x > float64(math.MaxInt32) {
		return math.MaxInt32
	}
	if x < float64(math.MinInt32) {
		return math.MinInt32
	}
	return int32(math.RoundToEven(x))
}

func float64ToInt16Round(x float64) int16 {
	if x > math.MaxInt16 {
		return math.MaxInt16
	}
	if x < math.MinInt16 {
		return math.MinInt16
	}
	return int16(math.RoundToEven(x))
}
