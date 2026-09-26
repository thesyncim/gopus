//go:build !arm64 || nosimd || !goexperiment.simd

package testvectors

// fusedFloat is false on the byte-exact builds: nosimd (rounding barrier in
// celt/fma32_arm64.go) and amd64 (no compiler FP contraction), where the CELT
// float path is byte-identical to scalar libopus and the same-arch byte-exact
// encode oracles apply.
const fusedFloat = false
