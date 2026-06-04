//go:build !purego

package multistream

// gopusBuildIsAsm reports whether this gopus build links the assembly/SIMD
// kernels (the default build: NEON on arm64, SSE/AVX on amd64). On amd64 the asm
// build is the strict bit-exact reference against the SIMD libopus oracle. The
// pure-Go build (-tags purego) sets this false and carries the documented ≤1-ULP
// CELT/Hybrid float boundary against the scalar libopus oracle. See
// project_arm64_celt_1ulp_drift.md.
const gopusBuildIsAsm = true
