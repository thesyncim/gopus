//go:build amd64 && goexperiment.simd && !nosimd

package celt

const libopusFloatInnerProdUsesSSEOrder = true
