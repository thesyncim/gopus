//go:build amd64.v3 && goexperiment.simd && !nosimd && !gopus_fixed_point

package encoder

const outerTargetV3SIMDFadeTail = true
