//go:build !arm64 || nosimd || !goexperiment.simd

package celt

const libopusFloatInnerProdUsesNeonOrder = false
