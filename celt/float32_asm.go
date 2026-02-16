//go:build arm64 || amd64

package celt

//go:noescape
func roundFloat64ToFloat32(x []float64)
