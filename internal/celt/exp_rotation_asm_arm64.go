//go:build arm64 && !purego && !goexperiment.simd

package celt

//go:noescape
func expRotation1PassNeon(x []float32, first, stride, blocks, dir int, c, s float32)
