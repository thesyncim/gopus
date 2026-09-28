package celt

import (
	"math"
	"math/rand"
	"testing"
)

// randBoundedKissCpx draws components across the whole range the Fast
// butterflies accept: zeros, subnormals, ordinary values and magnitudes just
// below 2^100.
func randBoundedKissCpx(rng *rand.Rand) kissCpx {
	f := func() float32 {
		switch rng.Intn(8) {
		case 0:
			return 0
		case 1:
			return math.Float32frombits(rng.Uint32() & 0x807fffff) // subnormal
		case 2:
			// Largest bounded exponents.
			return math.Float32frombits(rng.Uint32()&0x807fffff | uint32(0xe0+rng.Intn(3))<<23)
		default:
			return float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(12))))
		}
	}
	return kissCpx{f(), f()}
}

func TestKfBflyInnerFastMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x6b66))
	type bfly func(fout, w []kissCpx, m, N, mm, fstride int)
	cases := []struct {
		name       string
		radix      int
		fast, want bfly
	}{
		{"radix3", 3, func(fout, w []kissCpx, m, N, mm, fstride int) {
			kfBfly3InnerFast(fout, packKissTwiddles3(w, m, fstride), w[fstride*m].i, N, mm)
		}, kfBfly3InnerScalar},
		{"radix4", 4, func(fout, w []kissCpx, m, N, mm, fstride int) {
			kfBfly4InnerFast(fout, packKissTwiddles4(w, m, fstride), N, mm)
		}, kfBfly4InnerScalar},
		{"radix5", 5, func(fout, w []kissCpx, m, N, mm, fstride int) {
			kfBfly5InnerFast(fout, packKissTwiddles5(w, m, fstride), w[fstride*m], w[2*fstride*m], N, mm)
		}, kfBfly5InnerScalar},
	}
	for _, tc := range cases {
		for iter := 0; iter < 500; iter++ {
			m := 1 + rng.Intn(40)
			N := 1 + rng.Intn(6)
			mm := tc.radix*m + rng.Intn(3)
			fstride := 1 + rng.Intn(4)
			fout := make([]kissCpx, N*mm+tc.radix*m)
			for i := range fout {
				fout[i] = randBoundedKissCpx(rng)
			}
			// Twiddles are unit-magnitude rotations.
			w := make([]kissCpx, fstride*tc.radix*m+1)
			for i := range w {
				s, c := math.Sincos(rng.Float64() * 2 * math.Pi)
				w[i] = kissCpx{float32(c), float32(s)}
			}
			want := append([]kissCpx(nil), fout...)
			tc.want(want, w, m, N, mm, fstride)
			tc.fast(fout, w, m, N, mm, fstride)
			for i := range fout {
				if math.Float32bits(fout[i].r) != math.Float32bits(want[i].r) || math.Float32bits(fout[i].i) != math.Float32bits(want[i].i) {
					t.Fatalf("%s iter %d m=%d N=%d mm=%d fs=%d: out[%d]=%08x,%08x want %08x,%08x", tc.name, iter, m, N, mm, fstride, i,
						math.Float32bits(fout[i].r), math.Float32bits(fout[i].i), math.Float32bits(want[i].r), math.Float32bits(want[i].i))
				}
			}
		}
	}
}

func TestKissFFTInputBounded(t *testing.T) {
	limit := math.Float32frombits(kissFFTFastLimitBits)
	below := math.Nextafter32(limit, 0)
	for _, tc := range []struct {
		v    float32
		want bool
	}{
		{0, true}, {float32(math.Copysign(0, -1)), true}, {1, true}, {-1e20, true},
		{below, true}, {-below, true},
		{limit, false}, {-limit, false}, {math.MaxFloat32, false},
		{float32(math.Inf(1)), false}, {float32(math.Inf(-1)), false},
		{float32(math.NaN()), false}, {math.Float32frombits(0xffc00001), false},
	} {
		for pos := 0; pos < 5; pos++ {
			x := make([]kissCpx, 3)
			x[pos/2].r = 7
			if pos%2 == 0 {
				x[pos/2].r = tc.v
			} else {
				x[pos/2].i = tc.v
			}
			if got := kissFFTInputBounded(x); got != tc.want {
				t.Fatalf("kissFFTInputBounded with %08x at %d = %t, want %t", math.Float32bits(tc.v), pos, got, tc.want)
			}
		}
	}
}

// TestKissFFTBoundedFastPathMatchesScalar runs full FFTs at the largest
// bounded input magnitude and checks the per-product scalar butterflies agree.
func TestKissFFTBoundedFastPathMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(0x464654))
	for _, n := range []int{60, 120, 240, 480, 960} {
		st := getKissFFTState(n)
		for iter := 0; iter < 20; iter++ {
			in := make([]kissCpx, n)
			scale := math.Pow(2, float64(rng.Intn(100)))
			for i := range in {
				in[i] = kissCpx{float32(rng.NormFloat64() * scale / 4), float32(rng.NormFloat64() * scale / 4)}
			}
			if !kissFFTInputBounded(in) {
				continue
			}
			got := append([]kissCpx(nil), in...)
			want := append([]kissCpx(nil), in...)
			kissFFTStagesForTest(st, got, true)
			kissFFTStagesForTest(st, want, false)
			for i := range got {
				if math.Float32bits(got[i].r) != math.Float32bits(want[i].r) || math.Float32bits(got[i].i) != math.Float32bits(want[i].i) {
					t.Fatalf("n=%d iter %d: out[%d]=%v want %v", n, iter, i, got[i], want[i])
				}
			}
		}
	}
}

// kissFFTStagesForTest mirrors fftImpl's stage loop with the Fast butterflies
// forced on or off.
func kissFFTStagesForTest(st *kissFFTState, fout []kissCpx, fast bool) {
	L := 0
	for 2*L+1 < len(st.factors) {
		m := st.factors[2*L+1]
		L++
		if m == 1 {
			break
		}
	}
	m := st.factors[2*L-1]
	shift := max(st.shift, 0)
	for i := L - 1; i >= 0; i-- {
		m2 := 1
		if i != 0 {
			m2 = st.factors[2*i-1]
		}
		twFstride := st.fstride[i] << shift
		N := st.fstride[i]
		switch st.factors[2*i] {
		case 2:
			kfBfly2(fout, m, N)
		case 4:
			kfBfly4(fout, twFstride, st, i, m, N, m2, fast)
		case 3:
			kfBfly3(fout, twFstride, st, i, m, N, m2, fast)
		case 5:
			kfBfly5(fout, twFstride, st, i, m, N, m2, fast)
		}
		m = m2
	}
}

// TestKfBfly4M1CoreScalarMatchesReference checks the array-pointer radix-4
// m == 1 stage against the kf_bfly4 m == 1 loop of celt/kiss_fft.c written
// with indexed accesses, including non-finite inputs.
func TestKfBfly4M1CoreScalarMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0x4d31))
	pick := func() float32 {
		switch rng.Intn(16) {
		case 0:
			return float32(math.Inf(1 - 2*rng.Intn(2)))
		case 1:
			return float32(math.NaN())
		default:
			return float32(rng.NormFloat64() * math.Pow(10, float64(rng.Intn(20)-5)))
		}
	}
	for _, n := range []int{1, 2, 3, 15, 30, 60, 120, 240} {
		got := make([]kissCpx, 4*n)
		for i := range got {
			got[i] = kissCpx{pick(), pick()}
		}
		want := append([]kissCpx(nil), got...)
		for i := 0; i < 4*n; i += 4 {
			a0, a1, a2, a3 := want[i], want[i+1], want[i+2], want[i+3]
			s0r, s0i := a0.r-a2.r, a0.i-a2.i
			f0r, f0i := a0.r+a2.r, a0.i+a2.i
			s1r, s1i := a1.r+a3.r, a1.i+a3.i
			f2r, f2i := f0r-s1r, f0i-s1i
			f0r += s1r
			f0i += s1i
			s1r, s1i = a1.r-a3.r, a1.i-a3.i
			want[i] = kissCpx{f0r, f0i}
			want[i+1] = kissCpx{s0r + s1i, s0i - s1r}
			want[i+2] = kissCpx{f2r, f2i}
			want[i+3] = kissCpx{s0r - s1i, s0i + s1r}
		}
		kfBfly4M1CoreScalar(got, n)
		for i := range got {
			if math.Float32bits(got[i].r) != math.Float32bits(want[i].r) || math.Float32bits(got[i].i) != math.Float32bits(want[i].i) {
				t.Fatalf("n=%d: out[%d]=%v want %v", n, i, got[i], want[i])
			}
		}
	}
	fout := make([]kissCpx, 4*120)
	if allocs := testing.AllocsPerRun(100, func() { kfBfly4M1CoreScalar(fout, 120) }); allocs != 0 {
		t.Fatalf("steady-state allocations = %g, want 0", allocs)
	}
}
