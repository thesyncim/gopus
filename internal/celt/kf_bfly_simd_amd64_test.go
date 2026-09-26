//go:build amd64 && goexperiment.simd && !nosimd

package celt

import (
	"math"
	"math/rand"
	"simd/archsimd"
	"testing"
)

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
		fn     func([]kissCpx, []kissCpx, int, int, int, int)
		scalar func([]kissCpx, []kissCpx, int, int, int, int)
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
			initial := make([]kissCpx, n)
			for i := range initial {
				initial[i] = kissCpx{r: rng.Float32()*2 - 1, i: rng.Float32()*2 - 1}
			}
			w := make([]kissCpx, (tc.radix*shape.m-1)*shape.fstride+1)
			for i := range w {
				w[i] = kissCpx{r: rng.Float32()*2 - 1, i: rng.Float32()*2 - 1}
			}
			got, want := append([]kissCpx(nil), initial...), append([]kissCpx(nil), initial...)
			tc.fn(got, w, shape.m, shape.n, shape.mm, shape.fstride)
			tc.scalar(want, w, shape.m, shape.n, shape.mm, shape.fstride)
			for i := range want {
				if math.Float32bits(got[i].r) != math.Float32bits(want[i].r) ||
					math.Float32bits(got[i].i) != math.Float32bits(want[i].i) {
					t.Fatalf("fout[%d] = (%08x,%08x), want (%08x,%08x)", i,
						math.Float32bits(got[i].r), math.Float32bits(got[i].i),
						math.Float32bits(want[i].r), math.Float32bits(want[i].i))
				}
			}
		})
	}
}

func TestKfBflyInnerAMD64NoAllocs(t *testing.T) {
	if !archsimd.X86.AVX() {
		t.Skip("AVX is required by the amd64 butterfly SIMD path")
	}

	for _, tc := range []struct {
		name  string
		radix int
		fn    func([]kissCpx, []kissCpx, int, int, int, int)
	}{
		{"radix3", 3, kfBfly3Inner},
		{"radix4", 4, kfBfly4Inner},
		{"radix5", 5, kfBfly5Inner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const m, n, fstride = 8, 4, 8
			mm := tc.radix * m
			fout := make([]kissCpx, n*mm)
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
		got := make([]kissCpx, 4*n)
		for i := range got {
			got[i] = kissCpx{rng.Float32()*2 - 1, rng.Float32()*2 - 1}
			if rng.Intn(8) == 0 {
				got[i].r = float32(math.Copysign(0, float64(rng.Float32()-0.5)))
			}
			if rng.Intn(8) == 0 {
				got[i].i = got[(i*7)%len(got)].i
			}
		}
		want := append([]kissCpx(nil), got...)
		kfBfly4M1Core(got, n)
		kfBfly4M1CoreScalar(want, n)
		for k := range want {
			if math.Float32bits(got[k].r) != math.Float32bits(want[k].r) ||
				math.Float32bits(got[k].i) != math.Float32bits(want[k].i) {
				t.Fatalf("n=%d: fout[%d] = (%08x,%08x), want (%08x,%08x)", n, k,
					math.Float32bits(got[k].r), math.Float32bits(got[k].i),
					math.Float32bits(want[k].r), math.Float32bits(want[k].i))
			}
		}
	}
	fout := make([]kissCpx, 4*60)
	if allocs := testing.AllocsPerRun(100, func() { kfBfly4M1Core(fout, 60) }); allocs != 0 {
		t.Fatalf("steady-state allocations = %g, want 0", allocs)
	}
}

func BenchmarkKfBflyAMD64(b *testing.B) {
	for _, tc := range []struct {
		name          string
		radix         int
		m, n, fstride int
		fn            func([]kissCpx, []kissCpx, int, int, int, int)
		scalar        func([]kissCpx, []kissCpx, int, int, int, int)
	}{
		{"radix3_m32_N5", 3, 32, 5, 5, kfBfly3Inner, kfBfly3InnerScalar},
		{"radix4_m8_N15", 4, 8, 15, 15, kfBfly4Inner, kfBfly4InnerScalar},
		{"radix5_m96_N1", 5, 96, 1, 1, kfBfly5Inner, kfBfly5InnerScalar},
	} {
		mm := tc.radix * tc.m
		fout := make([]kissCpx, tc.n*mm)
		for i := range fout {
			fout[i] = kissCpx{0.25, -0.5}
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
	fout := make([]kissCpx, 4*120)
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
