package encoder

type opusVal16 = float32
type opusVal32 = float32
type opusRes = float32

func copyFloat64ToOpusRes(dst []opusRes, src []float64) {
	n := min(len(dst), len(src))
	for i := 0; i < n; i++ {
		dst[i] = opusRes(src[i])
	}
}
