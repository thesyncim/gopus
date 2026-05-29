//go:build arm64

package celt

import "math"

func fma32(a, b, c float32) float32 {
	return a*b + c
}

func mul32(a, b float32) float32 {
	return math.Float32frombits(math.Float32bits(a * b))
}

func add32(a, b float32) float32 {
	return math.Float32frombits(math.Float32bits(a + b))
}

func sub32(a, b float32) float32 {
	return math.Float32frombits(math.Float32bits(a - b))
}
