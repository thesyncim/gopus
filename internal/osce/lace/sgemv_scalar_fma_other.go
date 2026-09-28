//go:build !arm64 || (goexperiment.simd && !nosimd)

package lace

const scalarOSCEGenericFMA = false
