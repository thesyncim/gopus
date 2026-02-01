//go:build !arm64

package celt

// Stub implementations for optional optimized radix-2/4 butterflies.
// These keep the package buildable when no SIMD/asm helpers are present.

func kfBfly2M1Available() bool { return false }

func kfBfly4M1Available() bool { return false }

// kfBfly2M1 is a fallback used on non-arm64 platforms.
// It matches the m==1 path in kfBfly2.
func kfBfly2M1(fout []kissCpx, n int) {
	for i := 0; i < n; i++ {
		fout2 := fout[1]
		fout[1].r = fout[0].r - fout2.r
		fout[1].i = fout[0].i - fout2.i
		fout[0].r += fout2.r
		fout[0].i += fout2.i
		fout = fout[2:]
	}
}

// kfBfly4M1 is a fallback used on non-arm64 platforms.
// It matches the m==1 path in kfBfly4.
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
