//go:build !goexperiment.simd || nosimd

package multistream

// gopusBuildIsSIMD is false on the scalar Go path: no SIMD kernels are linked,
// so the matched-tier libopus reference is the scalar parity oracle, which the
// scalar path tracks to within the documented ≤1-ULP
// CELT/Hybrid float boundary. See project_arm64_celt_1ulp_drift.md.
const gopusBuildIsSIMD = false
