//go:build arm64 && !purego

package celt

//go:noescape
func fma32(a, b, c float32) float32

//go:noescape
func mul32(a, b float32) float32
