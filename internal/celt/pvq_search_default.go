//go:build !arm64 || !goexperiment.simd || nosimd || purego

package celt

func pvqExtractAbsSignNorm(x []celtNorm, absX []float32, y []float32, signx []byte, iy []int32, n int) {
	pvqExtractAbsSignNormScalar(x, absX, y, signx, iy, n)
}

func pvqExtractAbsSignOnlySum(x []celtNorm, absX []float32, signx []byte, n int) float32 {
	return pvqExtractAbsSignOnlySumScalar(x, absX, signx, n)
}

func pvqProjectHighPulse(absX, y []float32, iy []int32, rcp float32, n, pulsesLeft int) (float32, float32, int) {
	return pvqProjectHighPulseScalar(absX, y, iy, rcp, n, pulsesLeft)
}

func pvqRestoreSigns(iy []int32, signx []byte, n int) {
	pvqRestoreSignsScalar(iy, signx, n)
}

// pvqSearchPulseLoop places pulsesLeft pulses using the rate-distortion
// criterion. Go fallback for non-SIMD architectures.
func pvqSearchPulseLoop(absX, y []float32, iy []int32, xy, yy float32, n, pulsesLeft int) (float32, float32) {
	for i := 0; i < pulsesLeft; i++ {
		yy += 1

		// Find best position
		bestID := 0
		rxy := xy + absX[0]
		ryy := yy + y[0]
		bestNum := rxy * rxy
		bestDen := ryy
		// Scan two positions per iteration. Keeping the best numerator and
		// denominator updated after each position preserves the scalar
		// comparison order while reducing loop overhead on ARM64.
		j := 1
		for j < n-1 {
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}

			j++
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num = rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestDen = ryy
				bestNum = num
				bestID = j
			}
			j++
		}
		if j < n {
			rxy = xy + absX[j]
			ryy = yy + y[j]
			num := rxy * rxy
			if bestDen*num > ryy*bestNum {
				bestID = j
			}
		}

		// Update state
		xy += absX[bestID]
		yy += y[bestID]
		y[bestID] += 2
		iy[bestID]++
	}
	return xy, yy
}
