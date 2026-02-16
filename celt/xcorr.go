 package celt

func celtInnerProd(x, y []float64, length int) float64 {
	if length <= 0 {
		return 0
	}
	x = x[:length:length]
	y = y[:length:length]
	var s0, s1, s2, s3 float64
	i := 0
	n := len(x) - 3
	for ; i < n; i += 4 {
		s0 += x[i] * y[i]
		s1 += x[i+1] * y[i+1]
		s2 += x[i+2] * y[i+2]
		s3 += x[i+3] * y[i+3]
	}
	for ; i < len(x); i++ {
		s0 += x[i] * y[i]
	}
	return s0 + s1 + s2 + s3
}

func dualInnerProd(x, y1, y2 []float64, length int) (float64, float64) {
	if length <= 0 {
		return 0, 0
	}
	x = x[:length:length]
	y1 = y1[:length:length]
	y2 = y2[:length:length]
	var a0, a1, a2, a3 float64
	var b0, b1, b2, b3 float64
	i := 0
	n := len(x) - 3
	for ; i < n; i += 4 {
		a0 += x[i] * y1[i]
		b0 += x[i] * y2[i]
		a1 += x[i+1] * y1[i+1]
		b1 += x[i+1] * y2[i+1]
		a2 += x[i+2] * y1[i+2]
		b2 += x[i+2] * y2[i+2]
		a3 += x[i+3] * y1[i+3]
		b3 += x[i+3] * y2[i+3]
	}
	for ; i < len(x); i++ {
		a0 += x[i] * y1[i]
		b0 += x[i] * y2[i]
	}
	return a0 + a1 + a2 + a3, b0 + b1 + b2 + b3
}

