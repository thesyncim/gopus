//go:build gopus_silk_trace && linux && amd64 && !gopus_fixed_point && (!goexperiment.simd || nosimd)

package silk_test

const silkLPCTraceOracleUsesAVX2 = false
