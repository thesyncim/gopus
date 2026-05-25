package celt

// CELT's float build stores these codec-domain values as C float.
// Keep internal aliases explicit so state storage matches libopus width.
type celtNorm = float32
type celtSig = float32
type celtEner = float32
type celtGLog = float32
type opusVal16 = float32
type opusVal32 = float32
type opusRes = float32

// floor32ToInt mirrors libopus float-build floor() calls while keeping the
// expression rounded to C float before converting to an integer.
func floor32ToInt(v float32) int {
	i := int(v)
	if float32(i) > v {
		i--
	}
	return i
}

// CeltEner exposes CELT's float-build celt_ener width to sibling packages that
// need to carry CELT-owned band-energy scratch without widening it.
type CeltEner = celtEner

// CeltNorm exposes CELT's float-build celt_norm width to tests and sibling
// packages that need to pass normalized CELT vectors without widening them.
type CeltNorm = celtNorm

func ensureSigSlice(buf *[]celtSig, n int) []celtSig {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtSig, n)
	} else {
		*buf = (*buf)[:n]
		clear(*buf)
	}
	return (*buf)[:n]
}

func absSumSig(x []celtSig) opusVal32 {
	var sum opusVal32
	for _, v := range x {
		if v < 0 {
			sum -= opusVal32(v)
		} else {
			sum += opusVal32(v)
		}
	}
	return sum
}

func copySigToFloat64(dst []float64, src []celtSig) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = float64(src[i])
	}
}

func interleaveSigToFloat64(left, right []celtSig, dst []float64) {
	n := min(len(left), len(right))
	n = min(n, len(dst)/2)
	for i := 0; i < n; i++ {
		dst[2*i] = float64(left[i])
		dst[2*i+1] = float64(right[i])
	}
}

func interleaveSigToFloat32(left, right []celtSig, dst []float32) {
	n := min(len(left), len(right))
	n = min(n, len(dst)/2)
	for i := 0; i < n; i++ {
		dst[2*i] = float32(left[i])
		dst[2*i+1] = float32(right[i])
	}
}

func copyFloat64ToSig(dst []celtSig, src []float64) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = celtSig(src[i])
	}
}

func copyFloat32ToSig(dst []celtSig, src []float32) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = celtSig(src[i])
	}
}

func copySigToFloat32(dst []float32, src []celtSig) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = float32(src[i])
	}
}

func ensureNormSlice(buf *[]celtNorm, n int) []celtNorm {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtNorm, n)
	} else {
		*buf = (*buf)[:n]
		clear(*buf)
	}
	return (*buf)[:n]
}

func copyFloat64ToNorm(dst []celtNorm, src []float64) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = celtNorm(src[i])
	}
}

func copyNormToFloat64(dst []float64, src []celtNorm) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = float64(src[i])
	}
}

func ensureEnerSlice(buf *[]celtEner, n int) []celtEner {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtEner, n)
	} else {
		*buf = (*buf)[:n]
		clear(*buf)
	}
	return (*buf)[:n]
}

func ensureGLogSlice(buf *[]celtGLog, n int) []celtGLog {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtGLog, n)
	} else {
		*buf = (*buf)[:n]
		clear(*buf)
	}
	return (*buf)[:n]
}

func ensureGLogSliceNoClear(buf *[]celtGLog, n int) []celtGLog {
	if n <= 0 {
		return nil
	}
	if cap(*buf) < n {
		*buf = make([]celtGLog, n)
	} else {
		*buf = (*buf)[:n]
	}
	return (*buf)[:n]
}

func copyGLogToFloat64(dst []float64, src []celtGLog) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = float64(src[i])
	}
}

func copyFloat64ToGLog(dst []celtGLog, src []float64) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = celtGLog(src[i])
	}
}

func appendFloat64AsGLog(dst []celtGLog, src []float64) []celtGLog {
	if cap(dst) < len(src) {
		dst = make([]celtGLog, len(src))
	} else {
		dst = dst[:len(src)]
	}
	for i := range src {
		dst[i] = celtGLog(src[i])
	}
	return dst
}
