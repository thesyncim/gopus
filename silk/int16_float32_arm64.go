//go:build arm64

package silk

//go:noescape
func writeInt16AsFloat32Core(dst []float32, src []int16, n int)
