//go:build amd64 && !purego

package celt

import "golang.org/x/sys/cpu"

func kfBfly2M1Available() bool { return true }

func kfBfly4M1Available() bool { return false }

func kfBfly4MxAvailable() bool { return false }

func kfBfly3M1Available() bool { return false }

func kfBfly5M1Available() bool { return false }

var kfBfly2M1Impl = kfBfly2M1SSE2

func init() {
	if cpu.X86.HasAVX2 {
		kfBfly2M1Impl = kfBfly2M1AVX2
		return
	}
	if cpu.X86.HasAVX {
		kfBfly2M1Impl = kfBfly2M1AVX
	}
}

func kfBfly2M1(fout []kissCpx, n int) {
	if n <= 0 {
		return
	}
	kfBfly2M1Impl(fout, n)
}

//go:noescape
func kfBfly2M1SSE2(fout []kissCpx, n int)

//go:noescape
func kfBfly2M1AVX(fout []kissCpx, n int)

//go:noescape
func kfBfly2M1AVX2(fout []kissCpx, n int)

// kfBfly4M1 is a fallback used on amd64. It matches the m==1 path in kfBfly4.
func kfBfly4M1(fout []kissCpx, n int) {
	for i := 0; i < n; i++ {
		scratch0 := cSub(fout[0], fout[2])
		fout[0] = cAdd(fout[0], fout[2])
		scratch1 := cAdd(fout[1], fout[3])
		fout[2] = cSub(fout[0], scratch1)
		fout[0] = cAdd(fout[0], scratch1)
		scratch1 = cSub(fout[1], fout[3])
		fout[1].r = scratch0.r + scratch1.i
		fout[1].i = scratch0.i - scratch1.r
		fout[3].r = scratch0.r - scratch1.i
		fout[3].i = scratch0.i + scratch1.r
		fout = fout[4:]
	}
}

// kfBfly4Mx is a fallback used on amd64. It uses the Go implementation via kfBfly4.
func kfBfly4Mx(_ []kissCpx, _ []kissCpx, _ int, _ int, _ int, _ int) {}

// kfBfly3M1 is a stub used on amd64.
func kfBfly3M1(_ []kissCpx, _ []kissCpx, _ int, _ int, _ int) {}

// kfBfly5M1 is a stub used on amd64.
func kfBfly5M1(_ []kissCpx, _ []kissCpx, _ int, _ int, _ int) {}
