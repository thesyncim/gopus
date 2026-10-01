//go:build gopus_silk_trace && linux && amd64 && goexperiment.simd && !nosimd && !purego && !gopus_fixed_point

package silk_test

const silkLPCTraceOracleUsesAVX2 = true
