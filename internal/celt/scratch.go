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

func ensureByteSlice(buf *[]byte, n int) []byte {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]byte, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

func ensureComplexSlice(buf *[]complex128, n int) []complex128 {
	if n < 0 {
		n = 0
	}
	if cap(*buf) < n {
		*buf = make([]complex128, n)
	} else {
		*buf = (*buf)[:n]
	}
	return *buf
}

type bandDecodeScratch struct {
	left     []float64
	right    []float64
	collapse []byte
	norm     []float64
	lowband  []float64
}

type imdctScratch struct {
	fftIn  []complex128
	fftOut []complex128
	buf    []float64
}
