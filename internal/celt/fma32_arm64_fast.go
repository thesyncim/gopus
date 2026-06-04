//go:build arm64 && !purego

package celt

// The default arm64 build is quality-gated (opus_compare), not bit-exact with
// scalar C: it drops the Float32bits anti-fusion barrier so the compiler can
// contract a*b+c into FMADD, matching how libopus's own NEON kernels diverge
// from scalar libopus while staying inside the RFC 8251 conformance envelope.
// The bit-exact oracle is the purego build (fma32_arm64.go).
func fma32(a, b, c float32) float32 {
	return a*b + c
}

func mul32(a, b float32) float32 {
	return a * b
}

func add32(a, b float32) float32 {
	return a + b
}

func sub32(a, b float32) float32 {
	return a - b
}
