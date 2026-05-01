//go:build arm64

package celt

//go:noescape
func cwrsiFastCore(n, k int, i uint32, y []int) uint32

//go:noescape
func cwrsiFastCore32(n, k int, i uint32, y []int32) uint32

//go:noescape
func cwrsiFastCore32N8(k int, i uint32, y []int32) uint32

//go:nosplit
func cwrsiFastCore32N5(k int, i uint32, y []int32) uint32 {
	_ = y[4]
	yy, k, i := cwrsiFastCore32HeadStep(5, k, i, y, 0)
	var yyStep uint32
	yyStep, k, i = cwrsiFastCore32HeadStep(4, k, i, y, 1)
	yy += yyStep
	yyStep, k, i = cwrsiFastCore32HeadStep(3, k, i, y, 2)
	yy += yyStep

	p := uint32(2*k + 1)
	s := 0
	if i >= p {
		s = -1
		i -= p
	}
	k0 := k
	k = int((i + 1) >> 1)
	if k != 0 {
		i -= uint32(2*k - 1)
	}
	yj := k0 - k
	if s != 0 {
		yj = -yj
	}
	y[3] = int32(yj)
	yy += uint32(yj * yj)

	yj = k
	if i != 0 {
		yj = -k
	}
	y[4] = int32(yj)
	yy += uint32(yj * yj)

	return yy
}

//go:nosplit
func cwrsiFastCore32N6(k int, i uint32, y []int32) uint32 {
	_ = y[5]
	yy, k, i := cwrsiFastCore32HeadStep(6, k, i, y, 0)
	var yyStep uint32
	yyStep, k, i = cwrsiFastCore32HeadStep(5, k, i, y, 1)
	yy += yyStep
	yyStep, k, i = cwrsiFastCore32HeadStep(4, k, i, y, 2)
	yy += yyStep
	yyStep, k, i = cwrsiFastCore32HeadStep(3, k, i, y, 3)
	yy += yyStep

	p := uint32(2*k + 1)
	s := 0
	if i >= p {
		s = -1
		i -= p
	}
	k0 := k
	k = int((i + 1) >> 1)
	if k != 0 {
		i -= uint32(2*k - 1)
	}
	yj := k0 - k
	if s != 0 {
		yj = -yj
	}
	y[4] = int32(yj)
	yy += uint32(yj * yj)

	yj = k
	if i != 0 {
		yj = -k
	}
	y[5] = int32(yj)
	yy += uint32(yj * yj)

	return yy
}

//go:nosplit
//go:nosplit
func cwrsiFastCore32HeadStep(nCur, k int, i uint32, y []int32, j int) (uint32, int, uint32) {
	var p, q uint32
	var s int
	var k0, yj int

	if k >= nCur {
		p = pvqUDenseUnchecked(nCur, k+1)
		if i >= p {
			s = -1
			i -= p
		}

		k0 = k
		q = pvqUDenseUnchecked(nCur, nCur)
		if q > i {
			k = nCur
			for {
				k--
				p = pvqUDenseUnchecked(k, nCur)
				if p <= i {
					break
				}
			}
		} else {
			for p = pvqUDenseUnchecked(nCur, k); p > i; p = pvqUDenseUnchecked(nCur, k) {
				k--
			}
		}
		i -= p
		yj = k0 - k
		if s != 0 {
			yj = -yj
		}
		y[j] = int32(yj)
		return uint32(yj * yj), k, i
	}

	p = pvqUDenseUnchecked(k, nCur)
	q = pvqUDenseUnchecked(k+1, nCur)
	if p <= i && i < q {
		i -= p
		y[j] = 0
		return 0, k, i
	}

	if i >= q {
		s = -1
		i -= q
	}
	k0 = k
	for {
		k--
		p = pvqUDenseUnchecked(k, nCur)
		if p <= i {
			break
		}
	}
	i -= p
	yj = k0 - k
	if s != 0 {
		yj = -yj
	}
	y[j] = int32(yj)
	return uint32(yj * yj), k, i
}

//go:nosplit
func cwrsiFastCore32N9(k int, i uint32, y []int32) uint32 {
	_ = y[8]
	yy, k, i := cwrsiFastCore32HeadStep(9, k, i, y, 0)
	return yy + cwrsiFastCore32N8(k, i, y[1:9:9])
}

//go:nosplit
//go:nosplit
func cwrsiFastCore32N11(k int, i uint32, y []int32) uint32 {
	_ = y[10]
	yy, k, i := cwrsiFastCore32HeadStep(11, k, i, y, 0)
	var yyStep uint32
	yyStep, k, i = cwrsiFastCore32HeadStep(10, k, i, y, 1)
	yy += yyStep
	yyStep, k, i = cwrsiFastCore32HeadStep(9, k, i, y, 2)
	yy += yyStep
	return yy + cwrsiFastCore32N8(k, i, y[3:11:11])
}
