//go:build amd64.v3 && (gopus_fixed_point || gopus_qext || gopus_dred || gopus_osce || gopus_custom_modes)

package encoder

// analysisMLPGemmUseFMA is true on amd64.v3 builds outside the default
// float-core lane, matching Go's v3 FMA contraction in gemm_accum.
const analysisMLPGemmUseFMA = true
