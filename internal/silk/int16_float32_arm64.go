//go:build arm64 && !purego && !goexperiment.simd

package silk

//go:noescape
func writeInt16AsFloat32Core(dst []float32, src []int16, n int)
