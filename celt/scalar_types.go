package celt

// CELT's float build stores these codec-domain values as C float.
// Keep internal aliases explicit so state storage matches libopus width.
type celtNorm = float32
type celtSig = float32
type celtGLog = float32

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
