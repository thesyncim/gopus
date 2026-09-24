//go:build arm64 && goexperiment.simd && !nosimd

package celt

const libopusFloatInnerProdUsesNeonOrder = true
