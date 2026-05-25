//go:build !arm64 || purego

package celt

func fma32(a, b, c float32) float32 {
	return a*b + c
}

//go:noinline
func mul32(a, b float32) float32 {
	return a * b
}

//go:noinline
func add32(a, b float32) float32 {
	return a + b
}

//go:noinline
func sub32(a, b float32) float32 {
	return a - b
}
