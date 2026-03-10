//go:build !arm64

package celt

func kfBfly5N1(fout []kissCpx, tw []kissCpx, m, fstride int) {
	kfBfly5N1Generic(fout, tw, m, fstride)
}

func useKfBfly5N1(fstride int) bool {
	return true
}
