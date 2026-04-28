//go:build !arm64
// +build !arm64

package dnnmath

func reciprocalEstimate32(x float32) float32 {
	return 1 / x
}
