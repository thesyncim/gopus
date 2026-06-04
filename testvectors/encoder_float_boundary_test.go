package testvectors

import "runtime"

// encoderCELTFloatBoundaryBuild reports whether this build carries the documented
// ≤1-ULP CELT/Hybrid float-analysis boundary against the matched libopus parity
// oracle, so a near-tie CELT/Hybrid quantization can flip and produce a small
// byte (and, in VBR, length) divergence:
//
//   - darwin/arm64: the CELT forward float path fuses a*b+c into FMADD, differing
//     from clang -ffp-contract=on by ≤1 ULP per operation
//     (project_arm64_celt_1ulp_drift.md).
//   - amd64 pure-Go build (-tags purego, gopusBuildIsAsm==false): the Go amd64
//     float backend does not reproduce gcc's scalar CELT/Hybrid MDCT /
//     band-energy / pitch analysis bit-for-bit, so a handful of packets land one
//     ULP apart. The build-config-matrix gate links the SCALAR libopus parity
//     oracle (Go-amd64-float vs gcc-amd64-scalar), which is the same boundary
//     class the arm64 build carries against its own libopus build.
//
// The amd64 assembly/SIMD build (gopusBuildIsAsm==true) is the strict bit-exact
// reference: its SSE/AVX kernels are tuned to match the SIMD libopus the oracle
// links there, so a CELT/Hybrid byte divergence is real and stays a HARD FAIL.
// The SILK encoder core is integer/range-coded and byte-exact on every build, so
// SILK byte-exactness is asserted strictly regardless of this predicate.
func encoderCELTFloatBoundaryBuild() bool {
	return runtime.GOARCH == "arm64" || !gopusBuildIsAsm
}
