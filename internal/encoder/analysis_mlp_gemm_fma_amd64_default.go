//go:build amd64 && (!amd64.v3 || (!gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce && !gopus_custom_modes))

package encoder

// analysisMLPGemmUseFMA is false on non-v3 amd64 and on the default amd64.v3
// float-core lane, where libopus src/mlp.c:gemm_accum rounds each product
// before accumulation.
const analysisMLPGemmUseFMA = false
