package silk

import "math"

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
