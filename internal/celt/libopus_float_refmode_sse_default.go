//go:build !amd64 || nosimd || !goexperiment.simd

package celt

const libopusFloatInnerProdUsesSSEOrder = false
