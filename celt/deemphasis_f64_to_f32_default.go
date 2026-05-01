//go:build !arm64

package celt

func deemphasisStereoPlanarF64ToF32Core(dst []float32, left, right []float64, n int, scale, stateL, stateR, coef, verySmall float32) (float32, float32) {
	for i, j := 0, 0; i < n; i, j = i+1, j+2 {
		tmpL := float32(left[i]) + verySmall + stateL
		stateL = coef * tmpL
		dst[j] = tmpL * scale

		tmpR := float32(right[i]) + verySmall + stateR
		stateR = coef * tmpR
		dst[j+1] = tmpR * scale
	}
	return stateL, stateR
}
