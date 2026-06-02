//go:build purego

package testvectors

// gopusBuildIsAsm is false on the pure-Go build (-tags purego): no assembly/SIMD
// kernels are linked, so the matched-tier quality reference is the scalar libopus
// parity oracle, which the pure-Go path tracks ~bit-exactly.
const gopusBuildIsAsm = false
