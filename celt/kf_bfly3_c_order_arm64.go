//go:build arm64 && !purego

package celt

//go:noescape
func kfBfly3InnerCOrder(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)
