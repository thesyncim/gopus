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

// pvqSearchPulseLoop places pulsesLeft pulses using the rate-distortion
// criterion. Go fallback for non-SIMD architectures.
func pvqSearchPulseLoop(absX, y []float32, iy []int, xy, yy float64, n, pulsesLeft int) (float64, float64) {
	xyf := float32(xy)
	yyf := float32(yy)
	for i := 0; i < pulsesLeft; i++ {
		yyf += 1

		// Find best position
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

		// Update state
		xyf += absX[bestID]
		yyf += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}
	return float64(xyf), float64(yyf)
}

// pvqExtractAbsSign converts float64 input to float32 abs values and extracts signs.
// Go fallback for non-SIMD architectures.
func pvqExtractAbsSign(x []float64, absX []float32, y []float32, signx []int, iy []int, n int) {
	for j := 0; j < n; j++ {
		iy[j] = 0
		signx[j] = 0
		y[j] = 0
		xj := x[j]
		if xj < 0 {
			signx[j] = 1
			absX[j] = float32(-xj)
		} else {
			absX[j] = float32(xj)
		}
	}
}
