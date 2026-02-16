//go:build !arm64 && !amd64

package celt

import "math"

// absSum computes the sum of absolute values of x.
func absSum(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += math.Abs(v)
	}
	return sum
}
