//go:build !arm64 && !amd64

package celt

func kfBfly5N1CurrentReference(fout []kissCpx, tw []kissCpx, m, fstride int) {
	kfBfly5N1Generic(fout, tw, m, fstride)
}
