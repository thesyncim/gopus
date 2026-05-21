package celt

// haar1Stride1Generic applies the Haar butterfly to n0 consecutive pairs of
// float64 values. Computation is done in float32 precision (matching libopus)
// then widened back to float64 for storage.
func haar1Stride1Generic(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 1 + (n0-1)*2
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		haar1Pair(x, idx, idx+1, invSqrt2)
		idx += 2
	}
}

func haar1Pair(x []float64, idx0, idx1 int, invSqrt2 float32) {
	tmp1 := noFMA32Mul(invSqrt2, float32(x[idx0]))
	tmp2 := noFMA32Mul(invSqrt2, float32(x[idx1]))
	x[idx0] = float64(noFMA32Add(tmp1, tmp2))
	x[idx1] = float64(noFMA32Sub(tmp1, tmp2))
}

func haar1Stride2Generic(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 3 + (n0-1)*4
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		haar1Pair(x, idx, idx+2, invSqrt2)
		haar1Pair(x, idx+1, idx+3, invSqrt2)
		idx += 4
	}
}

func haar1Stride4(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 7 + (n0-1)*8
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		haar1Pair(x, idx, idx+4, invSqrt2)
		haar1Pair(x, idx+1, idx+5, invSqrt2)
		haar1Pair(x, idx+2, idx+6, invSqrt2)
		haar1Pair(x, idx+3, idx+7, invSqrt2)
		idx += 8
	}
}

func haar1Stride6(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 11 + (n0-1)*12
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		haar1Pair(x, idx, idx+6, invSqrt2)
		haar1Pair(x, idx+1, idx+7, invSqrt2)
		haar1Pair(x, idx+2, idx+8, invSqrt2)
		haar1Pair(x, idx+3, idx+9, invSqrt2)
		haar1Pair(x, idx+4, idx+10, invSqrt2)
		haar1Pair(x, idx+5, idx+11, invSqrt2)
		idx += 12
	}
}

func haar1Stride8(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 15 + (n0-1)*16
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		haar1Pair(x, idx, idx+8, invSqrt2)
		haar1Pair(x, idx+1, idx+9, invSqrt2)
		haar1Pair(x, idx+2, idx+10, invSqrt2)
		haar1Pair(x, idx+3, idx+11, invSqrt2)
		haar1Pair(x, idx+4, idx+12, invSqrt2)
		haar1Pair(x, idx+5, idx+13, invSqrt2)
		haar1Pair(x, idx+6, idx+14, invSqrt2)
		haar1Pair(x, idx+7, idx+15, invSqrt2)
		idx += 16
	}
}

func haar1Stride12(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 23 + (n0-1)*24
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	idx := 0
	for j := 0; j < n0; j++ {
		haar1Pair(x, idx, idx+12, invSqrt2)
		haar1Pair(x, idx+1, idx+13, invSqrt2)
		haar1Pair(x, idx+2, idx+14, invSqrt2)
		haar1Pair(x, idx+3, idx+15, invSqrt2)
		haar1Pair(x, idx+4, idx+16, invSqrt2)
		haar1Pair(x, idx+5, idx+17, invSqrt2)
		haar1Pair(x, idx+6, idx+18, invSqrt2)
		haar1Pair(x, idx+7, idx+19, invSqrt2)
		haar1Pair(x, idx+8, idx+20, invSqrt2)
		haar1Pair(x, idx+9, idx+21, invSqrt2)
		haar1Pair(x, idx+10, idx+22, invSqrt2)
		haar1Pair(x, idx+11, idx+23, invSqrt2)
		idx += 24
	}
}
