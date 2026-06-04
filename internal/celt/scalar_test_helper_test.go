package celt

func copyFloat64ToSig(dst []celtSig, src []float64) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = celtSig(src[i])
	}
}
