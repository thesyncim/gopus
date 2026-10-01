//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes && (!goexperiment.simd || nosimd || purego)

package encoder

const analysisSpecVariabilityModelSIMD = false
