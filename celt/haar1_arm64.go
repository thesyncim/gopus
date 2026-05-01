//go:build arm64 && !race && !purego

package celt

//go:noescape
func haar1Stride1Asm(x []float64, n0 int)

//go:noescape
func haar1Stride2Asm(x []float64, n0 int)

//go:noescape
func haar1Stride4Asm(x []float64, n0 int)
