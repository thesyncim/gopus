//go:build !goexperiment.simd || nosimd

package testvectors

// gopusBuildIsSIMD is false when the scalar Go path is selected, so the matched
// quality reference is the scalar libopus parity oracle.
const gopusBuildIsSIMD = false
