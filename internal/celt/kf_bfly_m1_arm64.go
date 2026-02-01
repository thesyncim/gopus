//go:build arm64

package celt

func kfBfly2M1Available() bool { return true }

func kfBfly4M1Available() bool { return false }

//go:noescape
func kfBfly2M1(fout []kissCpx, n int)

// kfBfly4M1 is a Go fallback for arm64 until a NEON float implementation lands.
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
