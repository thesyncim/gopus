//go:build !arm64 || purego

package celt

func fma32(a, b, c float32) float32 {
	return a*b + c
}

func mul32(a, b float32) float32 {
	return a * b
}

func add32(a, b float32) float32 {
	return a + b
}

func sub32(a, b float32) float32 {
	return a - b
}
