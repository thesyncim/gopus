//go:build amd64 && !goexperiment.simd && !nosimd

package celt

// The ordinary amd64 build uses the libopus SSE accumulation order in Go. The
// nosimd build keeps the scalar reference order.
const libopusFloatInnerProdUsesSSEOrder = true
