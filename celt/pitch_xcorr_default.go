//go:build !arm64 && !amd64

package celt

func celtPitchXcorr(x []float64, y []float64, xcorr []float64, length, maxPitch int) {
	if length <= 0 || maxPitch <= 0 {
		return
	}
	x = x[:length:length]
	xcorr = xcorr[:maxPitch:maxPitch]
	yLen := maxPitch + length - 1
	if yLen > len(y) {
		return
	}
	y = y[:yLen:yLen]
	i := 0
	for ; i+3 < maxPitch; i += 4 {
		yy0 := y[i : i+length : i+length]
		yy1 := y[i+1 : i+1+length : i+1+length]
		yy2 := y[i+2 : i+2+length : i+2+length]
		yy3 := y[i+3 : i+3+length : i+3+length]
		var s0, s1, s2, s3 float64
		for j := 0; j < len(x); j++ {
			xj := x[j]
			s0 += xj * yy0[j]
			s1 += xj * yy1[j]
			s2 += xj * yy2[j]
			s3 += xj * yy3[j]
		}
		xcorr[i] = s0
		xcorr[i+1] = s1
		xcorr[i+2] = s2
		xcorr[i+3] = s3
	}
	for ; i < maxPitch; i++ {
		yy := y[i : i+length : i+length]
		sum := 0.0
		for j := 0; j < len(x); j++ {
			sum += x[j] * yy[j]
		}
		xcorr[i] = sum
	}
}
