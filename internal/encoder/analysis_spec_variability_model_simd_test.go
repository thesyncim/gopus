//go:build linux && amd64.v3 && goexperiment.simd && !nosimd && !purego && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes

package encoder

const analysisSpecVariabilityModelSIMD = true
