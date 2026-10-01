//go:build !arm64 || !goexperiment.simd || nosimd || purego

package gopus

const pcmInt16VectorTiesAway = false
