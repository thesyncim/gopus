//go:build arm64 && !purego

package celt

// On arm64 the gc backend may contract a*b+c into a single FMADD. fma32 keeps
// that fusion where the code explicitly asks for it (the kiss-FFT twiddle hot
// path). mul32/add32/sub32 are the non-fused-intent primitives: they route
// through round32 so a materialized product cannot fuse with a surrounding
// add/sub, matching scalar libopus bit-exactly via the cheap float32-conversion
// barrier rather than the Float32bits round-trip the purego oracle uses
// (fma32_arm64.go).
func fma32(a, b, c float32) float32 {
	return a*b + c
}

func mul32(a, b float32) float32 {
	return round32(a * b)
}

func add32(a, b float32) float32 {
	return round32(a + b)
}

func sub32(a, b float32) float32 {
	return round32(a - b)
}
