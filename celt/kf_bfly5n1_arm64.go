//go:build arm64

package celt

//go:noescape
func kfBfly5N1(fout []kissCpx, tw []kissCpx, m, fstride int)
