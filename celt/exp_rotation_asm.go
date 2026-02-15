//go:build arm64 || amd64

package celt

//go:noescape
func expRotation1Stride2(x []float64, length int, c, s float64)
