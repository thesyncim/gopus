//go:build arm64 && !purego

package opusmath

func fma32(a, b, c float32) float32 {
	return a*b + c
}
