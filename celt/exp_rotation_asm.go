//go:build arm64

package celt

//go:noescape
func expRotation1Stride1(x []float64, length int, c, s float64)

//go:noescape
func expRotation1Stride2(x []float64, length int, c, s float64)
