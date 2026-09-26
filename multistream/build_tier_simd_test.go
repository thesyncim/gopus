//go:build goexperiment.simd && !nosimd

package multistream

// gopusBuildIsSIMD reports whether the Go SIMD build is selected.
const gopusBuildIsSIMD = true
