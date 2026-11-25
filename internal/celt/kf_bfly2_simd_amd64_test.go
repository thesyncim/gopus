//go:build amd64 && goexperiment.simd && !nosimd && !purego

package celt

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"math"
	"math/rand"
	"simd/archsimd"
	"testing"
)

func TestKfBfly2M4SIMDMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x2b4))
	pick := func() float32 {
		switch rng.Intn(12) {
		case 0:
			return float32(math.Copysign(0, float64(rng.Float32()-0.5)))
		case 1:
			return float32(math.Inf(1 - 2*rng.Intn(2)))
		default:
			return float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(12)-4)))
		}
	}
	for _, n := range []int{1, 2, 3, 15, 30, 60} {
		for iter := 0; iter < 50; iter++ {
			got := make([]float32, 16*n)
			for i := range got {
				got[i] = pick()
			}
			want := append([]float32(nil), got...)
			kfBfly2M4SIMD(got, n)
			kfBfly2M4SIMDReference(want, n)
			for k := range want {
				if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
					t.Fatalf("n=%d iter %d: fout[%d] = %08x, want %08x", n, iter, k,
						math.Float32bits(got[k]), math.Float32bits(want[k]))
				}
			}
		}
	}
	fout := make([]float32, 16*60)
	if allocs := testing.AllocsPerRun(100, func() { kfBfly2M4SIMD(fout, 60) }); allocs != 0 {
		t.Fatalf("steady-state allocations = %g, want 0", allocs)
	}
}

// kfBfly2M4SIMDReference follows the GCC target-v3 vector blocks in
// celt/kiss_fft.c::kf_bfly2: lanes 1 and 3 fuse twiddle products into outputs
// for each four-group block, while the final one to three groups round first.
func kfBfly2M4SIMDReference(fout []float32, n int) {
	tw := kfBfly2M4Twiddle
	fusedGroups := n &^ 3
	for group := 0; group < n; group++ {
		base := group * 16
		for lane := 0; lane < 4; lane++ {
			lo := base + 2*lane
			hi := base + 8 + 2*lane
			ar, ai := fout[lo], fout[lo+1]
			br, bi := fout[hi], fout[hi+1]
			var tr, ti float32
			switch lane {
			case 0:
				tr, ti = br, bi
			case 1:
				tr, ti = round32(br+bi), round32(bi-br)
			case 2:
				tr, ti = bi, -br
			case 3:
				tr, ti = round32(bi-br), -round32(bi+br)
			}
			if kissFFTTargetV3FMA && group < fusedGroups && (lane == 1 || lane == 3) {
				fout[hi] = opusmath.FMA32(-tw, tr, ar)
				fout[lo] = opusmath.FMA32(tw, tr, ar)
				fout[hi+1] = opusmath.FMA32(-tw, ti, ai)
				fout[lo+1] = opusmath.FMA32(tw, ti, ai)
				continue
			}
			tr, ti = round32(tr*tw), round32(ti*tw)
			if lane == 0 || lane == 2 {
				tr, ti = br, bi
				if lane == 2 {
					tr, ti = bi, -br
				}
			}
			fout[hi], fout[hi+1] = ar-tr, ai-ti
			fout[lo], fout[lo+1] = ar+tr, ai+ti
		}
	}
}

type amd64BflyShape struct {
	m, n, mm, fstride int
}

func TestKfBflyInnerAMD64MatchesScalar(t *testing.T) {
	if !archsimd.X86.AVX() {
		t.Skip("AVX is required by the amd64 butterfly SIMD path")
	}

	tests := []struct {
		name   string
		radix  int
		shape  amd64BflyShape
		fn     func([]float32, []kissCpx, int, int, int, int)
		scalar func([]float32, []kissCpx, int, int, int, int)
	}{
		{"radix4_m8_N15_fs15", 4, amd64BflyShape{8, 15, 32, 15}, kfBfly4Inner, kfBfly4InnerScalar},
		{"radix4_m4_N15_fs15", 4, amd64BflyShape{4, 15, 16, 15}, kfBfly4Inner, kfBfly4InnerScalar},
		{"radix4_m16_N1_fs1", 4, amd64BflyShape{16, 1, 64, 1}, kfBfly4Inner, kfBfly4InnerScalar},
		{"radix5_m96_N1_fs1", 5, amd64BflyShape{96, 1, 480, 1}, kfBfly5Inner, kfBfly5InnerScalar},
		{"radix5_m48_N1_fs1", 5, amd64BflyShape{48, 1, 240, 1}, kfBfly5Inner, kfBfly5InnerScalar},
		{"radix5_m24_N1_fs1", 5, amd64BflyShape{24, 1, 120, 1}, kfBfly5Inner, kfBfly5InnerScalar},
		{"radix5_m12_N1_fs1", 5, amd64BflyShape{12, 1, 60, 1}, kfBfly5Inner, kfBfly5InnerScalar},
		{"radix5_m8_N4_fs8", 5, amd64BflyShape{8, 4, 40, 8}, kfBfly5Inner, kfBfly5InnerScalar},
		{"radix3_m32_N5_fs5", 3, amd64BflyShape{32, 5, 96, 5}, kfBfly3Inner, kfBfly3InnerScalar},
		{"radix3_m16_N5_fs10", 3, amd64BflyShape{16, 5, 48, 10}, kfBfly3Inner, kfBfly3InnerScalar},
		{"radix3_m8_N5_fs20", 3, amd64BflyShape{8, 5, 24, 20}, kfBfly3Inner, kfBfly3InnerScalar},
		{"radix3_m4_N5_fs40", 3, amd64BflyShape{4, 5, 12, 40}, kfBfly3Inner, kfBfly3InnerScalar},
		{"radix3_m6_N2_fs1_scalar", 3, amd64BflyShape{6, 2, 18, 1}, kfBfly3Inner, kfBfly3InnerScalar},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			shape := tc.shape
			rng := rand.New(rand.NewSource(int64(tc.radix*1000003 + shape.m*101 + shape.n*13 + shape.fstride)))
			n := shape.n * shape.mm
			initial := make([]float32, 2*n)
			for i := range initial {
				initial[i] = rng.Float32()*2 - 1
			}
			w := make([]kissCpx, (tc.radix*shape.m-1)*shape.fstride+1)
			for i := range w {
				w[i] = kissCpx{r: rng.Float32()*2 - 1, i: rng.Float32()*2 - 1}
			}
			got, want := append([]float32(nil), initial...), append([]float32(nil), initial...)
			tc.fn(got, w, shape.m, shape.n, shape.mm, shape.fstride)
			if kissFFTTargetV3FMA && shape.m&3 == 0 {
				switch tc.radix {
				case 3:
					kfBfly3InnerSIMDV3Ref(want, w, shape.m, shape.n, shape.mm, shape.fstride)
				case 4:
					kfBfly4InnerSIMDV3Ref(want, w, shape.m, shape.n, shape.mm, shape.fstride)
				default:
					tc.scalar(want, w, shape.m, shape.n, shape.mm, shape.fstride)
				}
			} else {
				tc.scalar(want, w, shape.m, shape.n, shape.mm, shape.fstride)
			}
			for i := range want {
				if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
					t.Fatalf("fout[%d] = %08x, want %08x", i,
						math.Float32bits(got[i]), math.Float32bits(want[i]))
				}
			}
		})
	}
}

func refKissMulSubFMA(a, b, c, d float32) float32 {
	return opusmath.FMA32(a, b, -round32(c*d))
}

func refKissMulAddFMA(a, b, c, d float32) float32 {
	return opusmath.FMA32(a, b, round32(c*d))
}

func refKissCpxMulUnfused(a, b kissCpx) kissCpx {
	return kissCpx{
		r: round32(a.r*b.r) - round32(a.i*b.i),
		i: round32(a.r*b.i) + round32(a.i*b.r),
	}
}

func refKissCpxMulFused(a, b kissCpx) kissCpx {
	return kissCpx{
		r: refKissMulSubFMA(a.r, b.r, a.i, b.i),
		i: refKissMulAddFMA(a.r, b.i, a.i, b.r),
	}
}

func kfBfly3InnerSIMDV3Ref(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	m2 := 2 * m
	m4 := 4 * m
	mm2 := 2 * mm
	epi3i := w[fstride*m].i
	for i := 0; i < N; i++ {
		base := i * mm2
		for j := 0; j < m; j++ {
			idx0 := base + 2*j
			idx1, idx2 := idx0+m2, idx0+m4
			a0 := kissCpx{fout[idx0], fout[idx0+1]}
			s1 := refKissCpxMulFused(kissCpx{fout[idx1], fout[idx1+1]}, w[j*fstride])
			s2 := refKissCpxMulFused(kissCpx{fout[idx2], fout[idx2+1]}, w[j*2*fstride])
			s3 := kissCpx{s1.r + s2.r, s1.i + s2.i}
			s0 := kissCpx{s1.r - s2.r, s1.i - s2.i}
			f1 := kissCpx{
				r: opusmath.FMA32(-0.5, s3.r, a0.r),
				i: opusmath.FMA32(-0.5, s3.i, a0.i),
			}
			s0.r, s0.i = round32(s0.r*epi3i), round32(s0.i*epi3i)
			fout[idx0], fout[idx0+1] = a0.r+s3.r, a0.i+s3.i
			fout[idx1], fout[idx1+1] = f1.r-s0.i, f1.i+s0.r
			fout[idx2], fout[idx2+1] = f1.r+s0.i, f1.i-s0.r
		}
	}
}

func kfBfly4InnerSIMDV3Ref(fout []float32, w []kissCpx, m, N, mm, fstride int) {
	m2, m4, m6 := 2*m, 4*m, 6*m
	mm2 := 2 * mm
	for i := 0; i < N; i++ {
		base := i * mm2
		for j := 0; j < m; j++ {
			idx0 := base + 2*j
			idx1, idx2, idx3 := idx0+m2, idx0+m4, idx0+m6
			f0 := kissCpx{fout[idx0], fout[idx0+1]}
			b1 := kissCpx{fout[idx1], fout[idx1+1]}
			b2 := kissCpx{fout[idx2], fout[idx2+1]}
			b3 := kissCpx{fout[idx3], fout[idx3+1]}
			tw1, tw2, tw3 := w[j*fstride], w[j*2*fstride], w[j*3*fstride]
			s0u := refKissCpxMulUnfused(b1, tw1)
			s1u := refKissCpxMulUnfused(b2, tw2)
			s2u := refKissCpxMulUnfused(b3, tw3)
			s0f := refKissCpxMulFused(b1, tw1)
			s2f := refKissCpxMulFused(b3, tw3)
			s5 := kissCpx{f0.r - s1u.r, f0.i - s1u.i}
			f01 := kissCpx{f0.r + s1u.r, f0.i + s1u.i}
			s3 := kissCpx{s0u.r + s2u.r, s0u.i + s2u.i}
			s4 := kissCpx{s0f.r - s2f.r, s0f.i - s2f.i}
			fout[idx2], fout[idx2+1] = f01.r-s3.r, f01.i-s3.i
			fout[idx0], fout[idx0+1] = f01.r+s3.r, f01.i+s3.i
			fout[idx1], fout[idx1+1] = s5.r+s4.i, s5.i-s4.r
			fout[idx3], fout[idx3+1] = s5.r-s4.i, s5.i+s4.r
		}
	}
}

func TestKfBflyInnerAMD64NoAllocs(t *testing.T) {
	if !archsimd.X86.AVX() {
		t.Skip("AVX is required by the amd64 butterfly SIMD path")
	}

	for _, tc := range []struct {
		name  string
		radix int
		fn    func([]float32, []kissCpx, int, int, int, int)
	}{
		{"radix3", 3, kfBfly3Inner},
		{"radix4", 4, kfBfly4Inner},
		{"radix5", 5, kfBfly5Inner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const m, n, fstride = 8, 4, 8
			mm := tc.radix * m
			fout := make([]float32, 2*n*mm)
			w := make([]kissCpx, (tc.radix*m-1)*fstride+1)
			tc.fn(fout, w, m, n, mm, fstride)
			if allocs := testing.AllocsPerRun(100, func() {
				tc.fn(fout, w, m, n, mm, fstride)
			}); allocs != 0 {
				t.Fatalf("steady-state allocations = %g, want 0", allocs)
			}
		})
	}
}

func TestKfBfly4M1CoreAMD64MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for _, n := range []int{1, 2, 3, 15, 30, 60, 120} {
		got := make([]float32, 8*n)
		for i := range 4 * n {
			got[2*i] = rng.Float32()*2 - 1
			got[2*i+1] = rng.Float32()*2 - 1
			if rng.Intn(8) == 0 {
				got[2*i] = float32(math.Copysign(0, float64(rng.Float32()-0.5)))
			}
			if rng.Intn(8) == 0 {
				got[2*i+1] = got[2*((i*7)%(4*n))+1]
			}
		}
		want := append([]float32(nil), got...)
		kfBfly4M1Core(got, n)
		kfBfly4M1CoreScalar(want, n)
		for k := range want {
			if math.Float32bits(got[k]) != math.Float32bits(want[k]) {
				t.Fatalf("n=%d: fout[%d] = %08x, want %08x", n, k,
					math.Float32bits(got[k]), math.Float32bits(want[k]))
			}
		}
	}
	fout := make([]float32, 8*60)
	if allocs := testing.AllocsPerRun(100, func() { kfBfly4M1Core(fout, 60) }); allocs != 0 {
		t.Fatalf("steady-state allocations = %g, want 0", allocs)
	}
}

func BenchmarkKfBflyAMD64(b *testing.B) {
	for _, tc := range []struct {
		name          string
		radix         int
		m, n, fstride int
		fn            func([]float32, []kissCpx, int, int, int, int)
		scalar        func([]float32, []kissCpx, int, int, int, int)
	}{
		{"radix3_m32_N5", 3, 32, 5, 5, kfBfly3Inner, kfBfly3InnerScalar},
		{"radix4_m8_N15", 4, 8, 15, 15, kfBfly4Inner, kfBfly4InnerScalar},
		{"radix5_m96_N1", 5, 96, 1, 1, kfBfly5Inner, kfBfly5InnerScalar},
	} {
		mm := tc.radix * tc.m
		fout := make([]float32, 2*tc.n*mm)
		for i := range tc.n * mm {
			fout[2*i] = 0.25
			fout[2*i+1] = -0.5
		}
		w := make([]kissCpx, (tc.radix*tc.m-1)*tc.fstride+1)
		for i := range w {
			w[i] = kissCpx{0.7, 0.3}
		}
		b.Run(tc.name+"/simd", func(b *testing.B) {
			for b.Loop() {
				tc.fn(fout, w, tc.m, tc.n, mm, tc.fstride)
			}
		})
		b.Run(tc.name+"/scalar", func(b *testing.B) {
			for b.Loop() {
				tc.scalar(fout, w, tc.m, tc.n, mm, tc.fstride)
			}
		})
	}
	fout := make([]float32, 8*120)
	b.Run("radix4m1_n120/simd", func(b *testing.B) {
		for b.Loop() {
			kfBfly4M1Core(fout, 120)
		}
	})
	b.Run("radix4m1_n120/scalar", func(b *testing.B) {
		for b.Loop() {
			kfBfly4M1CoreScalar(fout, 120)
		}
	})
}
