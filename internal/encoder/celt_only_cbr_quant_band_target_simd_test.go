//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && goexperiment.simd && !nosimd && !purego

package encoder

const celtOnlyCBRQuantTraceFrame = 25

// At 20 ms, LM=3 gives M=8; pinned CELT eBands[7:8] spans one coefficient.
const celtOnlyCBRQuantTraceN = 8
const celtOnlyCBRQuantTraceLM = 3
const celtOnlyCBRQuantTraceDualStereoShape = true
