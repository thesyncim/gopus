//go:build !arm64 || purego
// +build !arm64 purego

package dnnmath

func reciprocalEstimate32(x float32) float32 {
	return 1 / x
}
