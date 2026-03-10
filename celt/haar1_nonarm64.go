//go:build !arm64

package celt

func haar1Stride1Asm(x []float64, n0 int) {
	haar1Stride1Generic(x, n0)
}

func haar1Stride2Asm(x []float64, n0 int) {
	haar1Stride2Generic(x, n0)
}
