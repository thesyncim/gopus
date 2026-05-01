//go:build !arm64 || purego

package gopus

func convertFloat32ToInt16Unit(dst []int16, src []float32, n int) bool {
	return false
}
