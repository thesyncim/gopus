//go:build arm64 && !purego

package celt

//go:noescape
func deemphasisStereoPlanarF64ToF32Core(dst []float32, left, right []float64, n int, scale, stateL, stateR, coef, verySmall float32) (float32, float32)
