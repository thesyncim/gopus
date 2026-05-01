//go:build arm64 && !purego

package celt

//go:noescape
func scaleFloat64Into(dst, src []float64, scale float64, n int)
