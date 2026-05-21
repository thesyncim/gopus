//go:build arm64 && !purego

package rdovae

//go:noescape
func fma32(a, b, c float32) float32
