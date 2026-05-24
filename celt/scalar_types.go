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

func copySigToFloat64(dst []float64, src []celtSig) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = float64(src[i])
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
