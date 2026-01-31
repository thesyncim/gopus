package celt

func ensureFloat64Slice(buf *[]float64, n int) []float64 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]float64, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureIntSlice(buf *[]int, n int) []int {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]int, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}
