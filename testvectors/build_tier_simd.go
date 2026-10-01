//go:build goexperiment.simd && !nosimd && !purego

package testvectors

// gopusBuildIsSIMD reports whether the Go SIMD build is selected. It uses the
// matching SIMD libopus quality reference.
const gopusBuildIsSIMD = true
