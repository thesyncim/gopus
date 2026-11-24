//go:build arm64 && !nosimd && !purego

package celt

import (
	"math"
	"math/rand"
	"testing"
)

func kfBfly4M1CoreRef(fout []float32, n int) {
	for i := 0; i < 8*n; i += 8 {
		a0r, a0i := fout[i], fout[i+1]
		a1r, a1i := fout[i+2], fout[i+3]
		a2r, a2i := fout[i+4], fout[i+5]
		a3r, a3i := fout[i+6], fout[i+7]

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
		fout[i], fout[i+1] = f0r, f0i
		fout[i+2], fout[i+3] = s0r+s1i, s0i-s1r
		fout[i+4], fout[i+5] = f2r, f2i
		fout[i+6], fout[i+7] = s0r-s1i, s0i+s1r
	}
}

// TestKfBfly4M1CoreBitExact pins the vectorized twiddle-free radix-4 kernel
// to the scalar reference bit-for-bit, including odd block counts that
// exercise the scalar tail and signed-zero inputs.
func TestKfBfly4M1CoreBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for _, n := range []int{1, 2, 3, 4, 7, 15, 60, 120} {
		got := make([]float32, 8*n)
		for i := range 4 * n {
			got[2*i] = rng.Float32()*2 - 1
			got[2*i+1] = rng.Float32()*2 - 1
			if rng.Intn(16) == 0 {
				got[2*i] = float32(math.Copysign(0, float64(rng.Float32()-0.5)))
			}
		}
		want := append([]float32(nil), got...)
		kfBfly4M1Core(got, n)
		kfBfly4M1CoreRef(want, n)
		for k := range want {
			if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
				t.Fatalf("n=%d: fout[%d] = %08x, want %08x", n, k,
					math.Float32bits(got[k]), math.Float32bits(want[k]))
			}
		}
	}
}
