//go:build amd64.v3 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// celtAMD64SIMDUseFMA is true on amd64.v3 builds, matching Go's v3 FMA
// contraction for float32 multiply-add expressions.
const celtAMD64SIMDUseFMA = true

func celtF32x4FMA(a, b, c archsimd.Float32x4) archsimd.Float32x4 {
	return a.MulAdd(b, c)
}
