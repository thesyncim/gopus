//go:build !arm64 || purego

package celt

func deemphasisStereoPlanarF32Core(dst []float32, left, right []float32, n int, scale, stateL, stateR, coef, verySmall float32) (float32, float32) {
	_ = dst[n*2-1]
	_ = left[n-1]
	_ = right[n-1]
	i := 0
	j := 0
	for ; i+3 < n; i, j = i+4, j+8 {
		tmpL0 := left[i] + verySmall + stateL
		stateL = coef * tmpL0
		tmpR0 := right[i] + verySmall + stateR
		stateR = coef * tmpR0
		dst[j] = tmpL0 * scale
		dst[j+1] = tmpR0 * scale

		tmpL1 := left[i+1] + verySmall + stateL
		stateL = coef * tmpL1
		tmpR1 := right[i+1] + verySmall + stateR
		stateR = coef * tmpR1
		dst[j+2] = tmpL1 * scale
		dst[j+3] = tmpR1 * scale

		tmpL2 := left[i+2] + verySmall + stateL
		stateL = coef * tmpL2
		tmpR2 := right[i+2] + verySmall + stateR
		stateR = coef * tmpR2
		dst[j+4] = tmpL2 * scale
		dst[j+5] = tmpR2 * scale

		tmpL3 := left[i+3] + verySmall + stateL
		stateL = coef * tmpL3
		tmpR3 := right[i+3] + verySmall + stateR
		stateR = coef * tmpR3
		dst[j+6] = tmpL3 * scale
		dst[j+7] = tmpR3 * scale
	}
	for ; i < n; i, j = i+1, j+2 {
		tmpL := left[i] + verySmall + stateL
		stateL = coef * tmpL
		tmpR := right[i] + verySmall + stateR
		stateR = coef * tmpR
		dst[j] = tmpL * scale
		dst[j+1] = tmpR * scale
	}
	return stateL, stateR
}
