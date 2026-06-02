//go:build !arm64 || purego

package testvectors

// fusedFloat is false on the byte-exact builds: purego (rounding barrier in
// celt/fma32_arm64.go) and amd64 (no compiler FP contraction), where the CELT
// float path is byte-identical to scalar libopus and the same-arch byte-exact
// encode oracles apply.
const fusedFloat = false
