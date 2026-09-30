//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && (!goexperiment.simd || nosimd || purego)

package encoder

const celtOnlyCBRSelectedFrame = 0
