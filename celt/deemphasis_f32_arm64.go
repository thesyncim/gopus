//go:build arm64

package celt

//go:noescape
func deemphasisStereoPlanarF32Core(dst []float32, left, right []float32, n int, scale, stateL, stateR, coef, verySmall float32) (float32, float32)
