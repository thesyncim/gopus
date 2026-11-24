//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

func kfBfly4M1Core(fout []float32, n int) {
	kfBfly4M1CoreSIMD(fout, n)
}

func kfBfly5Inner(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	kfBfly5InnerSIMD(fout, w, m, N, mm, fstride)
}

func kfBfly3Inner(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	kfBfly3InnerSIMD(fout, w, m, N, mm, fstride)
}

func kfBfly4Inner(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	kfBfly4InnerSIMD(fout, w, m, N, mm, fstride)
}

// kfBflyScalarFastInput is false: the SIMD butterflies handle every stage.
func kfBflyScalarFastInput([]float32) bool { return false }

func kfBfly2M4(fout []float32, n int) {
	kfBfly2M4SIMD(fout, n)
}
