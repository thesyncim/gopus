//go:build gopus_libopus_bench && goexperiment.simd && !nosimd && !purego

package gopus_test

// scoreboardGopusIsNoSimd is false for the Go SIMD build.
const scoreboardGopusIsNoSimd = false
