//go:build arm64

package gopus

//go:noescape
func convertFloat32ToInt16Unit(dst []int16, src []float32, n int) bool
