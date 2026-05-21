//go:build !arm64 || purego

package rdovae

import "math"

func fma32(a, b, c float32) float32 {
	return float32(math.FMA(float64(a), float64(b), float64(c)))
}
