//go:build arm64 && !purego

package celt

//go:noescape
func imdctPostRotateF32(buf []float32, trig []float32, n2, n4 int)
