//go:build !arm64

package celt

// cwrsiFastCore is the table-only CWRS decoder. Callers validate n/k/y and
// only enter this path for table-covered pairs.
//
//go:nosplit
func cwrsiFastCore(n, k int, i uint32, y []int) uint32 {
	_ = y[n-1]

	var yy uint32
	j := 0
	for nCur := n; nCur > 2; nCur-- {
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
			y[j] = yj
			yy += uint32(yj * yj)
		} else {
			p = pvqUDenseUnchecked(k, nCur)
			q = pvqUDenseUnchecked(k+1, nCur)

			if p <= i && i < q {
				i -= p
				y[j] = 0
			} else {
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
				y[j] = yj
				yy += uint32(yj * yj)
			}
		}
		j++
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
	y[j] = yj
	yy += uint32(yj * yj)
	j++

	s = -int(i)
	yj = k
	if s != 0 {
		yj = -k
	}
	y[j] = yj
	yy += uint32(yj * yj)

	return yy
}

// cwrsiFastCore32 is the decode-only int32 pulse variant of cwrsiFastCore.
//
//go:nosplit
func cwrsiFastCore32(n, k int, i uint32, y []int32) uint32 {
	_ = y[n-1]

	var yy uint32
	j := 0
	for nCur := n; nCur > 2; nCur-- {
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
			yy += uint32(yj * yj)
		} else {
			p = pvqUDenseUnchecked(k, nCur)
			q = pvqUDenseUnchecked(k+1, nCur)

			if p <= i && i < q {
				i -= p
				y[j] = 0
			} else {
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
				yy += uint32(yj * yj)
			}
		}
		j++
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
	y[j] = int32(yj)
	yy += uint32(yj * yj)
	j++

	s = -int(i)
	yj = k
	if s != 0 {
		yj = -k
	}
	y[j] = int32(yj)
	yy += uint32(yj * yj)

	return yy
}

func cwrsiFastCore32N8(k int, i uint32, y []int32) uint32 {
	return cwrsiFastCore32(8, k, i, y)
}

func cwrsiFastCore32N3(k int, i uint32, y []int32) uint32 {
	return cwrsiFastCore32(3, k, i, y)
}

func cwrsiFastCore32N4(k int, i uint32, y []int32) uint32 {
	return cwrsiFastCore32(4, k, i, y)
}

func cwrsiFastCore32N5(k int, i uint32, y []int32) uint32 {
	return cwrsiFastCore32(5, k, i, y)
}

func cwrsiFastCore32N6(k int, i uint32, y []int32) uint32 {
	return cwrsiFastCore32(6, k, i, y)
}

func cwrsiFastCore32N9(k int, i uint32, y []int32) uint32 {
	return cwrsiFastCore32(9, k, i, y)
}

func cwrsiFastCore32N11(k int, i uint32, y []int32) uint32 {
	return cwrsiFastCore32(11, k, i, y)
}

func cwrsiFastCore32N12(k int, i uint32, y []int32) uint32 {
	return cwrsiFastCore32(12, k, i, y)
}
