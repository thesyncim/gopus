package testvectors

import "math"

func maxAbsSlice(s []float64) float64 {
	max := 0.0
	for _, v := range s {
		if math.Abs(v) > max {
			max = math.Abs(v)
		}
	}
	return max
}
