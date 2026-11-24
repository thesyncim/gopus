//go:build amd64 && !amd64.v3 && goexperiment.simd && !nosimd && !purego

package celt

import "simd/archsimd"

// celtAMD64SIMDUseFMA is false on non-v3 amd64 builds, matching scalar float32
// multiply-then-add rounding without FMA contraction.
const celtAMD64SIMDUseFMA = false

func celtF32x4FMA(a, b, c archsimd.Float32x4) archsimd.Float32x4 {
	return a.Mul(b).Add(c)
}
