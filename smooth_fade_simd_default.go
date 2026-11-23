//go:build (!arm64 && !amd64) || !goexperiment.simd || nosimd || purego

package gopus

func smoothFade48kSIMD(in1, in2, out, win []float32, overlap, channels int) bool {
	return false
}
