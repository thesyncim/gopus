//go:build !arm64 && !amd64

package celt

// pvqSearchBestPos finds the position with the best rate-distortion score
// for placing a pulse.
func pvqSearchBestPos(absX, y []float32, xy, yy float64, n int) int {
	if n <= 0 {
		return 0
	}
	xyf := float32(xy)
	yyf := float32(yy)
	bestID := 0
	rxy := xyf + absX[0]
	ryy := yyf + y[0]
	bestNum := rxy * rxy
	bestDen := ryy
	for j := 1; j < n; j++ {
		rxy = xyf + absX[j]
		ryy = yyf + y[j]
		num := rxy * rxy
		if bestDen*num > ryy*bestNum {
			bestDen = ryy
			bestNum = num
			bestID = j
		}
	}
	return bestID
}
