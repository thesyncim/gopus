//go:build !purego

package testvectors

// gopusBuildIsAsm reports whether this gopus build links the assembly/SIMD
// kernels (the default build: NEON on arm64, SSE/AVX on amd64). It is the
// selector for the matched-tier quality reference: an asm gopus build must be
// compared against a SIMD libopus, not the scalar parity oracle.
const gopusBuildIsAsm = true
