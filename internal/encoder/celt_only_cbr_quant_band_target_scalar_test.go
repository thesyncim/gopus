//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext && (!goexperiment.simd || nosimd || purego)

package encoder

const celtOnlyCBRQuantTraceFrame = 0

// At 20 ms, LM=3 gives M=8; pinned CELT eBands[17:19]=[40,48], so N=8*M.
const celtOnlyCBRQuantTraceN = 64
const celtOnlyCBRQuantTraceLM = 3
const celtOnlyCBRQuantTraceDualStereoShape = false
