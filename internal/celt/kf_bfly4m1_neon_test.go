//go:build arm64 && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

func kfBfly4M1CoreRef(fout []kissCpx, n int) {
	for i := 0; i < 4*n; i += 4 {
		a0r, a0i := fout[i].r, fout[i].i
		a1r, a1i := fout[i+1].r, fout[i+1].i
		a2r, a2i := fout[i+2].r, fout[i+2].i
		a3r, a3i := fout[i+3].r, fout[i+3].i

		s0r := a0r - a2r
		s0i := a0i - a2i
		f0r := a0r + a2r
		f0i := a0i + a2i

		s1r := a1r + a3r
		s1i := a1i + a3i
		f2r := f0r - s1r
		f2i := f0i - s1i
		f0r += s1r
		f0i += s1i

		s1r = a1r - a3r
		s1i = a1i - a3i
		fout[i] = kissCpx{f0r, f0i}
		fout[i+1] = kissCpx{s0r + s1i, s0i - s1r}
		fout[i+2] = kissCpx{f2r, f2i}
		fout[i+3] = kissCpx{s0r - s1i, s0i + s1r}
	}
}

// TestKfBfly4M1CoreBitExact pins the vectorized twiddle-free radix-4 kernel
// to the scalar reference bit-for-bit, including odd block counts that
// exercise the scalar tail and signed-zero inputs.
func TestKfBfly4M1CoreBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for _, n := range []int{1, 2, 3, 4, 7, 15, 60, 120} {
		got := make([]kissCpx, 4*n)
		for i := range got {
			got[i] = kissCpx{rng.Float32()*2 - 1, rng.Float32()*2 - 1}
			if rng.Intn(16) == 0 {
				got[i].r = float32(math.Copysign(0, float64(rng.Float32()-0.5)))
			}
		}
		want := append([]kissCpx(nil), got...)
		kfBfly4M1Core(got, n)
		kfBfly4M1CoreRef(want, n)
		for k := range want {
			if math.Float32bits(got[k].r) != math.Float32bits(want[k].r) ||
				math.Float32bits(got[k].i) != math.Float32bits(want[k].i) {
				t.Fatalf("n=%d: fout[%d] = (%08x,%08x), want (%08x,%08x)", n, k,
					math.Float32bits(got[k].r), math.Float32bits(got[k].i),
					math.Float32bits(want[k].r), math.Float32bits(want[k].i))
			}
		}
	}
}
