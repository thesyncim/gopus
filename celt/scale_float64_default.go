//go:build !arm64

package celt

func scaleFloat64Into(dst, src []float64, scale float64, n int) {
	if n <= 0 {
		return
	}
	dst = dst[:n:n]
	src = src[:n:n]
	for i := 0; i < n; i++ {
		dst[i] = scale * src[i]
	}
}
