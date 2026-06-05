//go:build gopus_fixed_point

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestKFBfly2Oracle checks KFBfly2 against the libopus FIXED_POINT kf_bfly2
// radix-2 butterfly bit-for-bit. It requires a --enable-fixed-point libopus
// reference build (built on demand by the oracle harness).
func TestKFBfly2Oracle(t *testing.T) {
	libopustest.RequireOracle(t)

	groups := kfBfly2OracleGroups()
	in := make([]libopustest.KissFFTComplex, 0, groups*8)
	for _, s := range kfBfly2OracleSamples(groups) {
		in = append(in, libopustest.KissFFTComplex{R: s.R, I: s.I})
	}

	got, err := libopustest.ProbeKissFFTBfly2(in)
	if err != nil {
		libopustest.HelperUnavailable(t, "kiss fft", err)
		return
	}
	if len(got) != len(in) {
		t.Fatalf("oracle returned %d samples, want %d", len(got), len(in))
	}

	have := make([]FFTCpx, len(in))
	for i, s := range in {
		have[i] = FFTCpx{R: s.R, I: s.I}
	}
	KFBfly2(have, 0, groups)

	for i := range have {
		want := FFTCpx{R: got[i].R, I: got[i].I}
		if have[i] != want {
			t.Errorf("sample %d: KFBfly2 = {%d,%d}, libopus kf_bfly2 = {%d,%d}",
				i, have[i].R, have[i].I, want.R, want.I)
		}
	}
}

// kfBfly2OracleGroups returns the number of 8-sample groups exercised.
func kfBfly2OracleGroups() int { return 4096 }

// kfBfly2OracleSamples builds a deterministic input set covering structural
// edge cases (zeros, equal real/imag pairs that stress the rounding direction
// of the Q15 twiddle multiply, saturation-magnitude operands that exercise the
// wrapping ADD32_ovflw/SUB32_ovflw/NEG32_ovflw) plus a pseudo-random sweep.
func kfBfly2OracleSamples(groups int) []FFTCpx {
	out := make([]FFTCpx, 0, groups*8)

	extremes := []int32{
		0, 1, -1, 2, -2, 3, -3,
		32767, -32768, 65535, -65536,
		0x40000000, -0x40000000,
		0x7fffffff, -0x80000000,
		0x55555555, -0x55555555,
		0x12345678, -0x12345678,
		1 << 29, -(1 << 29), (1 << 30) - 1, 1 << 28,
	}

	push := func(r, i int32) { out = append(out, FFTCpx{R: r, I: i}) }

	// Deterministic structural samples: cross-product of extreme magnitudes,
	// padded to whole groups.
	for _, r := range extremes {
		for _, i := range extremes {
			push(r, i)
		}
	}

	rng := rand.New(rand.NewSource(0x6b66626c79320001))
	for len(out) < groups*8 {
		push(int32(rng.Uint32()), int32(rng.Uint32()))
	}
	return out[:groups*8]
}

// fftStage describes one butterfly stage as opus_fft_impl invokes it: the radix
// p, the per-group sub-length m, the previous sub-length mm, the twiddle stride
// fstride, and the number of groups N.
type fftStage struct {
	p, m, mm, fstride, n int
}

// kfFactorStages reproduces libopus kf_factor + the opus_fft_impl stride/stage
// loop (with st->shift == -1 so the effective shift is 0) to derive the exact
// (p, m, mm, fstride, N) parameters each kf_bfly* call receives for an nfft-point
// FFT. It returns nil if nfft cannot be factored into radices 2..5.
func kfFactorStages(nfft int) []fftStage {
	// kf_factor: populate facbuf as p1,m1,p2,m2,...
	const maxFactors = 8
	fac := make([]int, 2*maxFactors)
	stages := 0
	n := nfft
	nbak := n
	p := 4
	for {
		for n%p != 0 {
			switch p {
			case 4:
				p = 2
			case 2:
				p = 3
			default:
				p += 2
			}
			if p > 32000 || p*p > n {
				p = n
			}
		}
		n /= p
		if p > 5 {
			return nil
		}
		fac[2*stages] = p
		if p == 2 && stages > 1 {
			fac[2*stages] = 4
			fac[2] = 2
		}
		stages++
		if n <= 1 {
			break
		}
	}
	n = nbak
	for i := 0; i < stages/2; i++ {
		fac[2*i], fac[2*(stages-i-1)] = fac[2*(stages-i-1)], fac[2*i]
	}
	for i := 0; i < stages; i++ {
		n /= fac[2*i]
		fac[2*i+1] = n
	}

	// opus_fft_impl: build fstride and walk stages from L-1 down to 0.
	fstride := make([]int, maxFactors+1)
	fstride[0] = 1
	L := 0
	var m int
	for {
		p := fac[2*L]
		m = fac[2*L+1]
		fstride[L+1] = fstride[L] * p
		L++
		if m == 1 {
			break
		}
	}
	m = fac[2*L-1]
	var out []fftStage
	for i := L - 1; i >= 0; i-- {
		var m2 int
		if i != 0 {
			m2 = fac[2*i-1]
		} else {
			m2 = 1
		}
		out = append(out, fftStage{
			p:       fac[2*i],
			m:       m,
			mm:      m2,
			fstride: fstride[i], // shift == 0 for a top-level FFT
			n:       fstride[i],
		})
		m = m2
	}
	return out
}

// radixBuffer builds a deterministic nfft-sample complex buffer mixing
// structural extremes with a pseudo-random sweep, used to drive a radix kernel.
func radixBuffer(nfft int, seed int64) []libopustest.KissFFTComplex {
	buf := make([]libopustest.KissFFTComplex, nfft)
	extremes := []int32{
		0, 1, -1, 2, -2, 32767, -32768, 0x40000000, -0x40000000,
		0x7fffffff, -0x80000000, 0x12345678, -0x12345678, 1 << 29, 1 << 28,
	}
	rng := rand.New(rand.NewSource(seed))
	for i := range buf {
		if i < len(extremes) {
			buf[i] = libopustest.KissFFTComplex{R: extremes[i], I: extremes[(i+3)%len(extremes)]}
		} else {
			buf[i] = libopustest.KissFFTComplex{R: int32(rng.Uint32()), I: int32(rng.Uint32())}
		}
	}
	return buf
}

// runRadixStages drives every stage of radix p for the given nffts against the
// libopus oracle, comparing the Go kernel bit-for-bit. The oracle also returns
// the twiddle table it generated for nfft so the Go kernel consumes identical
// Q15 twiddles.
func runRadixStages(t *testing.T, mode uint32, wantRadix int, nffts []int,
	goKernel func(fout []FFTCpx, offset int, tw []FFTTwiddle, fstride, m, n, mm int)) {
	t.Helper()
	libopustest.RequireOracle(t)

	exercised := 0
	for _, nfft := range nffts {
		stages := kfFactorStages(nfft)
		if stages == nil {
			t.Fatalf("nfft=%d not factorable into radices 2..5", nfft)
		}
		for si, st := range stages {
			if st.p != wantRadix {
				continue
			}
			exercised++
			samples := radixBuffer(nfft, int64(0x726164697830000+int64(nfft*131+si)))

			res, err := libopustest.ProbeKissFFTBflyRadix(mode, uint32(nfft), st.fstride, st.m, st.n, st.mm, samples)
			if err != nil {
				libopustest.HelperUnavailable(t, "kiss fft", err)
				return
			}
			if len(res.Twiddles) != nfft {
				t.Fatalf("nfft=%d: oracle returned %d twiddles, want %d", nfft, len(res.Twiddles), nfft)
			}
			if len(res.Samples) != len(samples) {
				t.Fatalf("nfft=%d: oracle returned %d samples, want %d", nfft, len(res.Samples), len(samples))
			}

			tw := make([]FFTTwiddle, nfft)
			for i, w := range res.Twiddles {
				tw[i] = FFTTwiddle{R: w.R, I: w.I}
			}
			have := make([]FFTCpx, len(samples))
			for i, s := range samples {
				have[i] = FFTCpx{R: s.R, I: s.I}
			}
			goKernel(have, 0, tw, st.fstride, st.m, st.n, st.mm)

			for i := range have {
				want := FFTCpx{R: res.Samples[i].R, I: res.Samples[i].I}
				if have[i] != want {
					t.Fatalf("nfft=%d stage=%d radix=%d m=%d N=%d mm=%d fstride=%d: sample %d = {%d,%d}, libopus = {%d,%d}",
						nfft, si, st.p, st.m, st.n, st.mm, st.fstride, i,
						have[i].R, have[i].I, want.R, want.I)
				}
			}
		}
	}
	if exercised == 0 {
		t.Fatalf("no radix-%d stages exercised across %v", wantRadix, nffts)
	}
}

// fftTestSizes are FFT lengths covering each radix. 480/240/120/60 are the CELT
// frame FFT sizes (factored into radix-4 stages plus a radix-3 or radix-5
// stage); 64/16 are pure powers of two (radix-4 with the m==1 degenerate stage
// plus a radix-2); 36 forces a radix-3 stage with m>1.
func fftTestSizes() []int { return []int{16, 64, 240, 480, 60, 120, 36} }

// TestKFBfly4Oracle checks KFBfly4 (both the m==1 degenerate stage and the
// general C_MUL path) against libopus kf_bfly4 bit-for-bit.
func TestKFBfly4Oracle(t *testing.T) {
	runRadixStages(t, libopustest.KissFFTModeBfly4, 4, fftTestSizes(), KFBfly4)
}

// TestKFBfly3Oracle checks KFBfly3 against libopus kf_bfly3 bit-for-bit.
func TestKFBfly3Oracle(t *testing.T) {
	runRadixStages(t, libopustest.KissFFTModeBfly3, 3, []int{60, 120, 36, 240, 480}, KFBfly3)
}

// TestKFBfly5Oracle checks KFBfly5 against libopus kf_bfly5 bit-for-bit.
func TestKFBfly5Oracle(t *testing.T) {
	runRadixStages(t, libopustest.KissFFTModeBfly5, 5, []int{60, 120, 480, 240}, KFBfly5)
}
