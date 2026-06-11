//go:build arm64 && !purego

package celt

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// FMA-like reference helpers mirroring kissMulAddSource/kissMulSubSource on
// the fused arm64 path: round the second product, fuse the first multiply.
func refMulAdd(a, b, c, d float32) float32 {
	t := c * d
	return float32(math.FMA(float64(a), float64(b), float64(t)))
}

func refMulSub(a, b, c, d float32) float32 {
	t := c * d
	return float32(math.FMA(float64(a), float64(b), float64(-t)))
}

func kfBfly4InnerRef(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	for i := 0; i < N; i++ {
		base := i * mm
		tw1, tw2, tw3 := 0, 0, 0
		for j := 0; j < m; j++ {
			idx0 := base + j
			idx1, idx2, idx3 := idx0+m, idx0+2*m, idx0+3*m
			f0r, f0i := fout[idx0].r, fout[idx0].i
			b1, b2, b3 := fout[idx1], fout[idx2], fout[idx3]
			w1, w2, w3 := w[tw1], w[tw2], w[tw3]

			s0r := refMulSub(b1.r, w1.r, b1.i, w1.i)
			s0i := refMulAdd(b1.r, w1.i, b1.i, w1.r)
			s1r := refMulSub(b2.r, w2.r, b2.i, w2.i)
			s1i := refMulAdd(b2.r, w2.i, b2.i, w2.r)
			s2r := refMulSub(b3.r, w3.r, b3.i, w3.i)
			s2i := refMulAdd(b3.r, w3.i, b3.i, w3.r)

			s5r, s5i := f0r-s1r, f0i-s1i
			f0r += s1r
			f0i += s1i
			s3r, s3i := s0r+s2r, s0i+s2i
			s4r, s4i := s0r-s2r, s0i-s2i

			fout[idx2] = kissCpx{f0r - s3r, f0i - s3i}
			fout[idx0] = kissCpx{f0r + s3r, f0i + s3i}
			fout[idx1] = kissCpx{s5r + s4i, s5i - s4r}
			fout[idx3] = kissCpx{s5r - s4i, s5i + s4r}

			tw1 += fstride
			tw2 += 2 * fstride
			tw3 += 3 * fstride
		}
	}
}

func kfBfly3InnerRef(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	m2 := 2 * m
	epi3i := w[fstride*m].i
	for i := 0; i < N; i++ {
		base := i * mm
		tw1, tw2 := 0, 0
		for j := 0; j < m; j++ {
			idx0 := base + j
			idx1, idx2 := idx0+m, idx0+m2
			a0r, a0i := fout[idx0].r, fout[idx0].i
			b1, b2 := fout[idx1], fout[idx2]
			w1, w2 := w[tw1], w[tw2]

			s1r := refMulSub(b1.r, w1.r, b1.i, w1.i)
			s1i := refMulAdd(b1.r, w1.i, b1.i, w1.r)
			s2r := refMulSub(b2.r, w2.r, b2.i, w2.i)
			s2i := refMulAdd(b2.r, w2.i, b2.i, w2.r)

			s3r, s3i := s1r+s2r, s1i+s2i
			s0r, s0i := s1r-s2r, s1i-s2i

			tw1 += fstride
			tw2 += 2 * fstride

			// Explicit float32 conversions force the kissHalfSub/kissScaleMul
			// roundings: Go may fuse a multiply into a later add across
			// statements, and only a conversion is a rounding barrier.
			h3r := float32(0.5 * s3r)
			h3i := float32(0.5 * s3i)
			f1r := a0r - h3r
			f1i := a0i - h3i
			s0r = float32(s0r * epi3i)
			s0i = float32(s0i * epi3i)
			fout[idx0] = kissCpx{a0r + s3r, a0i + s3i}
			fout[idx2] = kissCpx{f1r + s0i, f1i - s0r}
			fout[idx1] = kissCpx{f1r - s0i, f1i + s0r}
		}
	}
}

func kfBfly5InnerRef(fout []kissCpx, w []kissCpx, m, N, mm, fstride int) {
	ya := w[fstride*m]
	yb := w[fstride*2*m]
	for i := 0; i < N; i++ {
		base := i * mm
		idx0, idx1, idx2, idx3, idx4 := base, base+m, base+2*m, base+3*m, base+4*m
		tw1, tw2, tw3, tw4 := 0, 0, 0, 0
		for u := 0; u < m; u++ {
			s0 := fout[idx0]
			b1, b2, b3, b4 := fout[idx1], fout[idx2], fout[idx3], fout[idx4]
			w1, w2, w3, w4 := w[tw1], w[tw2], w[tw3], w[tw4]

			s1r := refMulSub(b1.r, w1.r, b1.i, w1.i)
			s1i := refMulAdd(b1.r, w1.i, b1.i, w1.r)
			s2r := refMulSub(b2.r, w2.r, b2.i, w2.i)
			s2i := refMulAdd(b2.r, w2.i, b2.i, w2.r)
			s3r := refMulSub(b3.r, w3.r, b3.i, w3.i)
			s3i := refMulAdd(b3.r, w3.i, b3.i, w3.r)
			s4r := refMulSub(b4.r, w4.r, b4.i, w4.i)
			s4i := refMulAdd(b4.r, w4.i, b4.i, w4.r)

			s7r, s7i := s1r+s4r, s1i+s4i
			s10r, s10i := s1r-s4r, s1i-s4i
			s8r, s8i := s2r+s3r, s2i+s3i
			s9r, s9i := s2r-s3r, s2i-s3i

			fout[idx0].r = s0.r + (s7r + s8r)
			fout[idx0].i = s0.i + (s7i + s8i)

			s5r := s0.r + refMulAdd(s7r, ya.r, s8r, yb.r)
			s5i := s0.i + refMulAdd(s7i, ya.r, s8i, yb.r)
			s6r := refMulAdd(s10i, ya.i, s9i, yb.i)
			s6i := -refMulAdd(s10r, ya.i, s9r, yb.i)
			fout[idx1] = kissCpx{s5r - s6r, s5i - s6i}
			fout[idx4] = kissCpx{s5r + s6r, s5i + s6i}

			s11r := s0.r + refMulAdd(s7r, yb.r, s8r, ya.r)
			s11i := s0.i + refMulAdd(s7i, yb.r, s8i, ya.r)
			s12r := refMulSub(s9i, ya.i, s10i, yb.i)
			s12i := refMulSub(s10r, yb.i, s9r, ya.i)
			fout[idx2] = kissCpx{s11r + s12r, s11i + s12i}
			fout[idx3] = kissCpx{s11r - s12r, s11i - s12i}

			idx0++
			idx1++
			idx2++
			idx3++
			idx4++
			tw1 += fstride
			tw2 += 2 * fstride
			tw3 += 3 * fstride
			tw4 += 4 * fstride
		}
	}
}

type bflyShape struct {
	m, N, mm, fstride int
}

func runBflyCase(t *testing.T, radix int, s bflyShape,
	asm, ref func(fout []kissCpx, w []kissCpx, m, N, mm, fstride int)) {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(radix*1000003 + s.m*101 + s.N*13 + s.fstride)))
	n := (s.N-1)*s.mm + radix*s.m
	fout := make([]kissCpx, n)
	for i := range fout {
		fout[i] = kissCpx{rng.Float32()*2 - 1, rng.Float32()*2 - 1}
	}
	wlen := (radix-1)*s.fstride*(s.m-1) + s.fstride*(radix-1)*s.m + 1
	w := make([]kissCpx, wlen)
	for i := range w {
		w[i] = kissCpx{rng.Float32()*2 - 1, rng.Float32()*2 - 1}
	}
	got := make([]kissCpx, n)
	want := make([]kissCpx, n)
	copy(got, fout)
	copy(want, fout)
	asm(got, w, s.m, s.N, s.mm, s.fstride)
	ref(want, w, s.m, s.N, s.mm, s.fstride)
	for k := range want {
		if math.Float32bits(got[k].r) != math.Float32bits(want[k].r) ||
			math.Float32bits(got[k].i) != math.Float32bits(want[k].i) {
			t.Fatalf("radix%d %+v: fout[%d] = (%08x,%08x), want (%08x,%08x)",
				radix, s, k,
				math.Float32bits(got[k].r), math.Float32bits(got[k].i),
				math.Float32bits(want[k].r), math.Float32bits(want[k].i))
		}
	}
}

// TestKfBflyInnerMatchesFMAReference pins the arm64 butterfly kernels to the
// FMA-like scalar reference bit-for-bit over the production FFT shapes
// (nfft 60/120/240/480 stages) plus off-grid shapes that exercise the
// vector/scalar tail split.
func TestKfBflyInnerMatchesFMAReference(t *testing.T) {
	shapes4 := []bflyShape{
		{m: 8, N: 15, mm: 32, fstride: 15},  // nfft=480 stage
		{m: 4, N: 15, mm: 16, fstride: 15},  // nfft=240 stage
		{m: 2, N: 15, mm: 8, fstride: 15},   // nfft=120 stage (scalar only)
		{m: 5, N: 3, mm: 20, fstride: 3},    // off-grid: blocks+tail
		{m: 9, N: 2, mm: 36, fstride: 2},    // off-grid
		{m: 16, N: 1, mm: 64, fstride: 1},   // contiguous twiddles
		{m: 7, N: 4, mm: 28, fstride: 4},    // tail 3
	}
	for _, s := range shapes4 {
		t.Run(fmt.Sprintf("radix4_m%d_N%d_fs%d", s.m, s.N, s.fstride), func(t *testing.T) {
			runBflyCase(t, 4, s, kfBfly4Inner, kfBfly4InnerRef)
		})
	}
	shapes3 := []bflyShape{
		{m: 32, N: 5, mm: 96, fstride: 5}, // nfft=480 stage
		{m: 16, N: 5, mm: 48, fstride: 5}, // nfft=240 stage
		{m: 8, N: 5, mm: 24, fstride: 5},  // nfft=120 stage
		{m: 4, N: 5, mm: 12, fstride: 5},  // nfft=60 stage
		{m: 6, N: 3, mm: 18, fstride: 3},  // off-grid: blocks+tail
		{m: 3, N: 2, mm: 9, fstride: 7},   // scalar only
	}
	for _, s := range shapes3 {
		t.Run(fmt.Sprintf("radix3_m%d_N%d_fs%d", s.m, s.N, s.fstride), func(t *testing.T) {
			runBflyCase(t, 3, s, kfBfly3Inner, kfBfly3InnerRef)
		})
	}
	shapes5 := []bflyShape{
		{m: 96, N: 1, mm: 480, fstride: 1}, // nfft=480 stage
		{m: 48, N: 1, mm: 240, fstride: 1}, // nfft=240 stage
		{m: 24, N: 1, mm: 120, fstride: 1}, // nfft=120 stage
		{m: 12, N: 1, mm: 60, fstride: 1},  // nfft=60 stage
		{m: 7, N: 2, mm: 35, fstride: 2},   // off-grid: blocks+tail
		{m: 3, N: 1, mm: 15, fstride: 3},   // scalar only
	}
	for _, s := range shapes5 {
		t.Run(fmt.Sprintf("radix5_m%d_N%d_fs%d", s.m, s.N, s.fstride), func(t *testing.T) {
			runBflyCase(t, 5, s, kfBfly5Inner, kfBfly5InnerRef)
		})
	}
}
