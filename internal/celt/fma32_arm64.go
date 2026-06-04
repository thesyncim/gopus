//go:build arm64 && purego

package celt

import "math"

// purego arm64 is the bit-exact Tier-1 oracle: the Float32bits round-trip is a
// rounding barrier that stops the arm64 backend contracting a*b+c into a single
// FMADD, so the scalar Go path stays byte-identical to scalar libopus. The
// default arm64 (asm) build uses fma32_arm64_fast.go, which lets the compiler
// fuse for libopus-NEON-class speed under the quality bar.
func fma32(a, b, c float32) float32 {
	return a*b + c
}

func mul32(a, b float32) float32 {
	return math.Float32frombits(math.Float32bits(a * b))
}

func add32(a, b float32) float32 {
	return math.Float32frombits(math.Float32bits(a + b))
}

func sub32(a, b float32) float32 {
	return math.Float32frombits(math.Float32bits(a - b))
}
