//go:build arm64 || amd64

package celt

// sumOfSquaresF64toF32 converts float64 elements to float32 and accumulates
// the sum of squares as float32. Returns the float32 result as float64.
// This matches the libopus pattern: sum += (float)x[i] * (float)x[i].
//
//go:noescape
func sumOfSquaresF64toF32(x []float64, n int) float64
