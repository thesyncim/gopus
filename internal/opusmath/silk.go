package opusmath

import "math"

// CReal marks the rare libopus FLP helpers whose source intentionally
// uses C double even in a float build. Examples include silk_energy_FLP(),
// silk_inner_product_FLP_c(), silk_burg_modified_FLP(), silk_schur_FLP(),
// warped_autocorrelation_FLP(), and corrMatrix_FLP().
type CReal = float64

func RoundF32HalfAwayFromZeroToInt32(x float32) int32 {
	if x >= 0 {
		return int32(math.Floor(float64(x) + 0.5))
	}
	return int32(math.Ceil(float64(x) - 0.5))
}

func ExpF32(x float32) float32 {
	return float32(math.Exp(float64(x)))
}

func Exp2F32(x float32) float32 {
	return float32(math.Exp2(float64(x)))
}

func Pow10F32(x float32) float32 {
	return float32(math.Pow(10, float64(x)))
}

func SinF32(x float32) float32 {
	return float32(math.Sin(float64(x)))
}

func CosF32(x float32) float32 {
	return float32(math.Cos(float64(x)))
}

func AtanF32(x float32) float32 {
	return float32(math.Atan(float64(x)))
}

func Exp2DivIntF32(x float32, denom int) float32 {
	return float32(math.Exp2(float64(x)) / float64(denom))
}

func Log2F32(x float32) float32 {
	return float32(math.Log2(float64(x)))
}

func LogF32(x float32) float32 {
	return float32(math.Log(float64(x)))
}

func Log10F32(x float32) float32 {
	return float32(math.Log10(float64(x)))
}

// SilkLog2F32 matches silk/float/SigProc_FLP.h silk_log2().
func SilkLog2F32(x float32) float32 {
	return float32(3.32192809488736 * math.Log10(float64(x)))
}

func SqrtF32(x float32) float32 {
	return float32(math.Sqrt(float64(x)))
}

func FloorHalfPlusF32ToInt32(x float32) int32 {
	return int32(math.Floor(float64(x) + 0.5))
}

func RoundToEvenF32ToInt32(x float32) int32 {
	return int32(math.RoundToEven(float64(x)))
}
