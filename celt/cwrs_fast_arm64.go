//go:build arm64

package celt

//go:noescape
func cwrsiFastCore(n, k int, i uint32, y []int) uint32

//go:noescape
func cwrsiFastCore32(n, k int, i uint32, y []int32) uint32

//go:noescape
func cwrsiFastCore32N8(k int, i uint32, y []int32) uint32

//go:noescape
func cwrsiFastCore32N3(k int, i uint32, y []int32) uint32

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
	var yy uint32

	if k >= 6 {
		p := pvqUDenseUnchecked(6, k+1)
		s := 0
		if i >= p {
			s = -1
			i -= p
		}

		k0 := k
		q := pvqUDenseUnchecked(6, 6)
		if q > i {
			k = 6
			for {
				k--
				p = pvqUDenseUnchecked(k, 6)
				if p <= i {
					break
				}
			}
		} else {
			for p = pvqUDenseUnchecked(6, k); p > i; p = pvqUDenseUnchecked(6, k) {
				k--
			}
		}
		i -= p
		yj := k0 - k
		if s != 0 {
			yj = -yj
		}
		y[0] = int32(yj)
		yy += uint32(yj * yj)
	} else {
		p := pvqUDenseUnchecked(k, 6)
		q := pvqUDenseUnchecked(k+1, 6)
		if p <= i && i < q {
			i -= p
			y[0] = 0
		} else {
			s := 0
			if i >= q {
				s = -1
				i -= q
			}
			k0 := k
			for {
				k--
				p = pvqUDenseUnchecked(k, 6)
				if p <= i {
					break
				}
			}
			i -= p
			yj := k0 - k
			if s != 0 {
				yj = -yj
			}
			y[0] = int32(yj)
			yy += uint32(yj * yj)
		}
	}

	if k >= 5 {
		p := pvqUDenseUnchecked(5, k+1)
		s := 0
		if i >= p {
			s = -1
			i -= p
		}

		k0 := k
		q := pvqUDenseUnchecked(5, 5)
		if q > i {
			k = 5
			for {
				k--
				p = pvqUDenseUnchecked(k, 5)
				if p <= i {
					break
				}
			}
		} else {
			for p = pvqUDenseUnchecked(5, k); p > i; p = pvqUDenseUnchecked(5, k) {
				k--
			}
		}
		i -= p
		yj := k0 - k
		if s != 0 {
			yj = -yj
		}
		y[1] = int32(yj)
		yy += uint32(yj * yj)
	} else {
		p := pvqUDenseUnchecked(k, 5)
		q := pvqUDenseUnchecked(k+1, 5)
		if p <= i && i < q {
			i -= p
			y[1] = 0
		} else {
			s := 0
			if i >= q {
				s = -1
				i -= q
			}
			k0 := k
			for {
				k--
				p = pvqUDenseUnchecked(k, 5)
				if p <= i {
					break
				}
			}
			i -= p
			yj := k0 - k
			if s != 0 {
				yj = -yj
			}
			y[1] = int32(yj)
			yy += uint32(yj * yj)
		}
	}

	return yy + cwrsiFastCore32N4(k, i, y[2:6:6])
}

//go:nosplit
func cwrsiFastCore32N4(k int, i uint32, y []int32) uint32 {
	_ = y[3]
	var yy uint32

	if k >= 4 {
		p := pvqUDenseUnchecked(4, k+1)
		s := 0
		if i >= p {
			s = -1
			i -= p
		}

		k0 := k
		q := pvqUDenseUnchecked(4, 4)
		if q > i {
			k = 4
			for {
				k--
				p = pvqUDenseUnchecked(k, 4)
				if p <= i {
					break
				}
			}
		} else {
			for p = pvqUDenseUnchecked(4, k); p > i; p = pvqUDenseUnchecked(4, k) {
				k--
			}
		}
		i -= p
		yj := k0 - k
		if s != 0 {
			yj = -yj
		}
		y[0] = int32(yj)
		yy += uint32(yj * yj)
	} else {
		p := pvqUDenseUnchecked(k, 4)
		q := pvqUDenseUnchecked(k+1, 4)
		if p <= i && i < q {
			i -= p
			y[0] = 0
		} else {
			s := 0
			if i >= q {
				s = -1
				i -= q
			}
			k0 := k
			for {
				k--
				p = pvqUDenseUnchecked(k, 4)
				if p <= i {
					break
				}
			}
			i -= p
			yj := k0 - k
			if s != 0 {
				yj = -yj
			}
			y[0] = int32(yj)
			yy += uint32(yj * yj)
		}
	}

	if k >= 3 {
		p := pvqUDenseUnchecked(3, k+1)
		s := 0
		if i >= p {
			s = -1
			i -= p
		}

		k0 := k
		q := pvqUDenseUnchecked(3, 3)
		if q > i {
			k = 3
			for {
				k--
				p = pvqUDenseUnchecked(k, 3)
				if p <= i {
					break
				}
			}
		} else {
			for p = pvqUDenseUnchecked(3, k); p > i; p = pvqUDenseUnchecked(3, k) {
				k--
			}
		}
		i -= p
		yj := k0 - k
		if s != 0 {
			yj = -yj
		}
		y[1] = int32(yj)
		yy += uint32(yj * yj)
	} else {
		p := pvqUDenseUnchecked(k, 3)
		q := pvqUDenseUnchecked(k+1, 3)
		if p <= i && i < q {
			i -= p
			y[1] = 0
		} else {
			s := 0
			if i >= q {
				s = -1
				i -= q
			}
			k0 := k
			for {
				k--
				p = pvqUDenseUnchecked(k, 3)
				if p <= i {
					break
				}
			}
			i -= p
			yj := k0 - k
			if s != 0 {
				yj = -yj
			}
			y[1] = int32(yj)
			yy += uint32(yj * yj)
		}
	}

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
	y[2] = int32(yj)
	yy += uint32(yj * yj)

	yj = k
	if i != 0 {
		yj = -k
	}
	y[3] = int32(yj)
	yy += uint32(yj * yj)

	return yy
}

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

	var yy uint32
	if k >= 9 {
		p := pvqUDenseUnchecked(9, k+1)
		s := 0
		if i >= p {
			s = -1
			i -= p
		}

		k0 := k
		q := pvqUDenseUnchecked(9, 9)
		if q > i {
			k = 9
			for {
				k--
				p = pvqUDenseUnchecked(k, 9)
				if p <= i {
					break
				}
			}
		} else {
			for p = pvqUDenseUnchecked(9, k); p > i; p = pvqUDenseUnchecked(9, k) {
				k--
			}
		}
		i -= p
		yj := k0 - k
		if s != 0 {
			yj = -yj
		}
		y[0] = int32(yj)
		yy += uint32(yj * yj)
	} else {
		p := pvqUDenseUnchecked(k, 9)
		q := pvqUDenseUnchecked(k+1, 9)
		if p <= i && i < q {
			i -= p
			y[0] = 0
		} else {
			s := 0
			if i >= q {
				s = -1
				i -= q
			}
			k0 := k
			for {
				k--
				p = pvqUDenseUnchecked(k, 9)
				if p <= i {
					break
				}
			}
			i -= p
			yj := k0 - k
			if s != 0 {
				yj = -yj
			}
			y[0] = int32(yj)
			yy += uint32(yj * yj)
		}
	}

	return yy + cwrsiFastCore32N8(k, i, y[1:9:9])
}

//go:nosplit
func cwrsiFastCore32N11(k int, i uint32, y []int32) uint32 {
	_ = y[10]

	var yy uint32
	if k >= 11 {
		p := pvqUDenseUnchecked(11, k+1)
		s := 0
		if i >= p {
			s = -1
			i -= p
		}

		k0 := k
		q := pvqUDenseUnchecked(11, 11)
		if q > i {
			k = 11
			for {
				k--
				p = pvqUDenseUnchecked(k, 11)
				if p <= i {
					break
				}
			}
		} else {
			for p = pvqUDenseUnchecked(11, k); p > i; p = pvqUDenseUnchecked(11, k) {
				k--
			}
		}
		i -= p
		yj := k0 - k
		if s != 0 {
			yj = -yj
		}
		y[0] = int32(yj)
		yy += uint32(yj * yj)
	} else {
		p := pvqUDenseUnchecked(k, 11)
		q := pvqUDenseUnchecked(k+1, 11)
		if p <= i && i < q {
			i -= p
			y[0] = 0
		} else {
			s := 0
			if i >= q {
				s = -1
				i -= q
			}
			k0 := k
			for {
				k--
				p = pvqUDenseUnchecked(k, 11)
				if p <= i {
					break
				}
			}
			i -= p
			yj := k0 - k
			if s != 0 {
				yj = -yj
			}
			y[0] = int32(yj)
			yy += uint32(yj * yj)
		}
	}

	if k >= 10 {
		p := pvqUDenseUnchecked(10, k+1)
		s := 0
		if i >= p {
			s = -1
			i -= p
		}

		k0 := k
		q := pvqUDenseUnchecked(10, 10)
		if q > i {
			k = 10
			for {
				k--
				p = pvqUDenseUnchecked(k, 10)
				if p <= i {
					break
				}
			}
		} else {
			for p = pvqUDenseUnchecked(10, k); p > i; p = pvqUDenseUnchecked(10, k) {
				k--
			}
		}
		i -= p
		yj := k0 - k
		if s != 0 {
			yj = -yj
		}
		y[1] = int32(yj)
		yy += uint32(yj * yj)
	} else {
		p := pvqUDenseUnchecked(k, 10)
		q := pvqUDenseUnchecked(k+1, 10)
		if p <= i && i < q {
			i -= p
			y[1] = 0
		} else {
			s := 0
			if i >= q {
				s = -1
				i -= q
			}
			k0 := k
			for {
				k--
				p = pvqUDenseUnchecked(k, 10)
				if p <= i {
					break
				}
			}
			i -= p
			yj := k0 - k
			if s != 0 {
				yj = -yj
			}
			y[1] = int32(yj)
			yy += uint32(yj * yj)
		}
	}

	return yy + cwrsiFastCore32N9(k, i, y[2:11:11])
}

//go:nosplit
func cwrsiFastCore32N12(k int, i uint32, y []int32) uint32 {
	_ = y[11]

	var yy uint32
	if k >= 12 {
		p := pvqUDenseUnchecked(12, k+1)
		s := 0
		if i >= p {
			s = -1
			i -= p
		}

		k0 := k
		q := pvqUDenseUnchecked(12, 12)
		if q > i {
			k = 12
			for {
				k--
				p = pvqUDenseUnchecked(k, 12)
				if p <= i {
					break
				}
			}
		} else {
			for p = pvqUDenseUnchecked(12, k); p > i; p = pvqUDenseUnchecked(12, k) {
				k--
			}
		}
		i -= p
		yj := k0 - k
		if s != 0 {
			yj = -yj
		}
		y[0] = int32(yj)
		yy += uint32(yj * yj)
	} else {
		p := pvqUDenseUnchecked(k, 12)
		q := pvqUDenseUnchecked(k+1, 12)
		if p <= i && i < q {
			i -= p
			y[0] = 0
		} else {
			s := 0
			if i >= q {
				s = -1
				i -= q
			}
			k0 := k
			for {
				k--
				p = pvqUDenseUnchecked(k, 12)
				if p <= i {
					break
				}
			}
			i -= p
			yj := k0 - k
			if s != 0 {
				yj = -yj
			}
			y[0] = int32(yj)
			yy += uint32(yj * yj)
		}
	}

	return yy + cwrsiFastCore32N11(k, i, y[1:12:12])
}
