//go:build arm64 && !purego

package celt

func kfBfly2M1Available() bool { return true }

func kfBfly4M1Available() bool { return true }

func kfBfly4MxAvailable() bool { return true }

func kfBfly3M1Available() bool { return true }

func kfBfly5M1Available() bool { return true }

//go:noescape
func kfBfly2M1(fout []kissCpx, n int)

//go:noescape
func kfBfly4M1(fout []kissCpx, n int)

//go:noescape
func kfBfly4Mx(fout []kissCpx, tw []kissCpx, m, n, fstride, mm int)

//go:noescape
func kfBfly3M1(fout []kissCpx, tw []kissCpx, fstride, n, mm int)

//go:noescape
func kfBfly5M1(fout []kissCpx, tw []kissCpx, fstride, n, mm int)
