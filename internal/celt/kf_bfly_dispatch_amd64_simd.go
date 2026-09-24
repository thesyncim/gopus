//go:build amd64 && goexperiment.simd && !nosimd

package celt

func kfBfly4M1Core(fout []kissCpx, n int) {
	kfBfly4M1CoreScalar(fout, n)
}

func kfBfly5Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly5InnerSIMD(fout, w, m, N, mm, fstride)
}

func kfBfly3Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly3InnerScalar(fout, w, m, N, mm, fstride)
}

func kfBfly4Inner(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly4InnerSIMD(fout, w, m, N, mm, fstride)
}
