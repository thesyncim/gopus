//go:build !arm64 || purego

package celt

func widenFloat32ToFloat64(dst []float64, src []float32, n int) {
	for i := 0; i < n; i++ {
		dst[i] = float64(src[i])
	}
}
