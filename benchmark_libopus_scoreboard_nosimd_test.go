//go:build gopus_libopus_bench && (!goexperiment.simd || nosimd)

package gopus_test

// scoreboardGopusIsNoSimd reports whether the scalar Go path is selected.
const scoreboardGopusIsNoSimd = true
