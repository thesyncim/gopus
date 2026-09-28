//go:build arm64 && (!goexperiment.simd || nosimd)

package bwe

func accumulateBWENorm(norm, kernel float32) float32 {
	return mulAdd32(kernel, kernel, norm)
}
