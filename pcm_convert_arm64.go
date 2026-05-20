//go:build arm64 && !purego

package gopus

//go:noescape
func convertFloat32ToInt16UnitBlocks(dst []int16, src []float32, n int) bool

func convertFloat32ToInt16Unit(dst []int16, src []float32, n int) bool {
	blocks := n &^ 15
	if blocks > 0 && !convertFloat32ToInt16UnitBlocks(dst, src, blocks) {
		return false
	}
	for i := blocks; i < n; i++ {
		v := src[i]
		if !(v >= -1 && v <= 1) {
			return false
		}
		dst[i] = float32ToInt16(v)
	}
	return true
}
