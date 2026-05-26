package celt

func haar1Stride1Generic(x []float64, n0 int) {
	const invSqrt2 = float32(0.7071067811865476)
	if n0 <= 0 {
		return
	}
	maxIdx := 1 + (n0-1)*2
	if maxIdx >= len(x) {
		return
	}
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
	idx := 0
	for j := 0; j < n0; j++ {
		haar1Pair(x, idx, idx+4, invSqrt2)
		haar1Pair(x, idx+1, idx+5, invSqrt2)
		haar1Pair(x, idx+2, idx+6, invSqrt2)
		haar1Pair(x, idx+3, idx+7, invSqrt2)
		idx += 8
	}
}

func haar1Stride1Asm(x []float64, n0 int) {
	haar1Stride1Generic(x, n0)
}

func haar1Stride2Asm(x []float64, n0 int) {
	haar1Stride2Generic(x, n0)
}

func haar1Stride4Asm(x []float64, n0 int) {
	haar1Stride4(x, n0)
}
