//go:build !arm64 || !goexperiment.simd || nosimd

package gopus

const pcmInt16VectorTiesAway = false
