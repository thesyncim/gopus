//go:build arm64 && !purego

package celt

//go:noescape
func roundFloat64ToFloat32(x []float64)

//go:noescape
func widenFloat32ToFloat64(dst []float64, src []float32, n int)
