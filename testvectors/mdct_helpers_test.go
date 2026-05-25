package testvectors

import "math"

func maxAbsSlice[S ~float32 | ~float64](s []S) float64 {
	max := 0.0
	for _, v := range s {
		abs := math.Abs(float64(v))
		if abs > max {
			max = abs
		}
	}
	return max
}
