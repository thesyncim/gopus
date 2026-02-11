//go:build !arm64 && !amd64

package celt

func celtInnerProd(x, y []float64, length int) float64 {
	if length <= 0 {
		return 0
	}
	_ = x[length-1] // BCE
	_ = y[length-1] // BCE
	sum := 0.0
	for i := 0; i < length; i++ {
		sum += x[i] * y[i]
	}
	return sum
}

func dualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	if length <= 0 {
		return 0, 0
	}
	_ = x[length-1]  // BCE
	_ = y1[length-1] // BCE
	_ = y2[length-1] // BCE
	sum1 := 0.0
	sum2 := 0.0
	for i := 0; i < length; i++ {
		sum1 += x[i] * y1[i]
		sum2 += x[i] * y2[i]
	}
	return sum1, sum2
}

func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	_ = x[length-1]          // BCE
	_ = xcorr[maxPitch-1]    // BCE
	_ = y[maxPitch+length-2] // BCE
	i := 0
	for ; i+3 < maxPitch; i += 4 {
		var s0, s1, s2, s3 float64
		for j := 0; j < length; j++ {
			xj := x[j]
			s0 += xj * y[i+j]
			s1 += xj * y[i+1+j]
			s2 += xj * y[i+2+j]
			s3 += xj * y[i+3+j]
		}
		xcorr[i] = s0
		xcorr[i+1] = s1
		xcorr[i+2] = s2
		xcorr[i+3] = s3
	}
	for ; i < maxPitch; i++ {
		sum := 0.0
		for j := 0; j < length; j++ {
			sum += x[j] * y[i+j]
		}
		xcorr[i] = sum
	}
}
