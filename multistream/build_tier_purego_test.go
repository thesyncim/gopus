//go:build purego

package multistream

// gopusBuildIsAsm is false on the pure-Go build (-tags purego): no assembly/SIMD
// kernels are linked, so the matched-tier libopus reference is the scalar parity
// oracle, which the pure-Go path tracks to within the documented ≤1-ULP
// CELT/Hybrid float boundary. See project_arm64_celt_1ulp_drift.md.
const gopusBuildIsAsm = false
