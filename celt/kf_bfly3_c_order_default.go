//go:build !arm64 || purego

package celt

func kfBfly3InnerCOrder(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	kfBfly3InnerCOrderGeneric(fout, w, m, N, mm, fstride)
}
