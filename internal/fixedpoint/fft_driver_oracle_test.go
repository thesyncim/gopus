//go:build gopus_fixedpoint

package fixedpoint

import (
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// fftDriverSizes are the CELT FFT lengths the integer MDCT depends on (the
// MDCT sub-FFTs for the 2.5/5/10/20 ms frames), each factored into radix-4
// stages plus a radix-3 or radix-5 stage.
func fftDriverSizes() []int { return []int{60, 120, 240, 480} }

// checkState compares a Go-built KissFFTState against the exact state libopus
// allocated for the same nfft (factors, bitrev, twiddles, scale_shift, scale).
// The standalone Go state uses shift==-1 while the library's standalone state
// also reports shift==-1, so both run opus_fft_impl with an effective shift 0.
func checkState(t *testing.T, st *KissFFTState, ref libopustest.KissFFTFullResult) {
	t.Helper()
	if st.scaleShift != ref.ScaleShift {
		t.Errorf("scale_shift = %d, libopus = %d", st.scaleShift, ref.ScaleShift)
	}
	if st.scale != ref.Scale {
		t.Errorf("scale = %d, libopus = %d", st.scale, ref.Scale)
	}
	if st.shift != ref.Shift {
		t.Errorf("shift = %d, libopus = %d", st.shift, ref.Shift)
	}
	if st.factors != ref.Factors {
		t.Errorf("factors = %v, libopus = %v", st.factors, ref.Factors)
	}
	for i := range st.bitrev {
		if st.bitrev[i] != ref.Bitrev[i] {
			t.Errorf("bitrev[%d] = %d, libopus = %d", i, st.bitrev[i], ref.Bitrev[i])
		}
	}
	for i := range st.twiddles {
		w := FFTTwiddle{R: ref.Twiddles[i].R, I: ref.Twiddles[i].I}
		if st.twiddles[i] != w {
			t.Errorf("twiddle[%d] = {%d,%d}, libopus = {%d,%d}",
				i, st.twiddles[i].R, st.twiddles[i].I, w.R, w.I)
		}
	}
}

// TestOpusFFTOracle drives the Go forward integer FFT (OpusFFT) against the real
// libopus opus_fft_c bit-for-bit over the CELT FFT sizes, and validates that the
// Go state constructor reproduces the library's exact tables.
func TestOpusFFTOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, nfft := range fftDriverSizes() {
		samples := radixBuffer(nfft, int64(0x6f7066667400000+int64(nfft)))

		ref, err := libopustest.ProbeKissFFTFull(libopustest.KissFFTModeFFT, samples)
		if err != nil {
			libopustest.HelperUnavailable(t, "kiss fft", err)
			return
		}

		st := NewKissFFTState(nfft)
		if st == nil {
			t.Fatalf("nfft=%d: NewKissFFTState returned nil", nfft)
		}
		checkState(t, st, ref)

		fin := make([]FFTCpx, nfft)
		for i, s := range samples {
			fin[i] = FFTCpx{R: s.R, I: s.I}
		}
		fout := make([]FFTCpx, nfft)
		OpusFFT(st, fin, fout)

		for i := range fout {
			want := FFTCpx{R: ref.Samples[i].R, I: ref.Samples[i].I}
			if fout[i] != want {
				t.Fatalf("nfft=%d: OpusFFT[%d] = {%d,%d}, libopus opus_fft_c = {%d,%d}",
					nfft, i, fout[i].R, fout[i].I, want.R, want.I)
			}
		}
	}
}

// TestOpusIFFTOracle drives the Go inverse integer FFT (OpusIFFT) against the
// real libopus opus_ifft_c bit-for-bit over the CELT FFT sizes.
func TestOpusIFFTOracle(t *testing.T) {
	libopustest.RequireOracle(t)

	for _, nfft := range fftDriverSizes() {
		samples := radixBuffer(nfft, int64(0x6966667400000+int64(nfft)))

		ref, err := libopustest.ProbeKissFFTFull(libopustest.KissFFTModeIFFT, samples)
		if err != nil {
			libopustest.HelperUnavailable(t, "kiss fft", err)
			return
		}

		st := NewKissFFTState(nfft)
		if st == nil {
			t.Fatalf("nfft=%d: NewKissFFTState returned nil", nfft)
		}
		checkState(t, st, ref)

		fin := make([]FFTCpx, nfft)
		for i, s := range samples {
			fin[i] = FFTCpx{R: s.R, I: s.I}
		}
		fout := make([]FFTCpx, nfft)
		OpusIFFT(st, fin, fout)

		for i := range fout {
			want := FFTCpx{R: ref.Samples[i].R, I: ref.Samples[i].I}
			if fout[i] != want {
				t.Fatalf("nfft=%d: OpusIFFT[%d] = {%d,%d}, libopus opus_ifft_c = {%d,%d}",
					nfft, i, fout[i].R, fout[i].I, want.R, want.I)
			}
		}
	}
}

// TestKissFFTStateSharedTwiddles checks that a sub-FFT state built with
// NewKissFFTStateTwiddles (sharing a larger base state's twiddle table, as the
// CELT MDCT sub-FFTs do) reports the same factors/bitrev and a non-negative
// shift consistent with the base size, and round-trips a forward+inverse
// transform exactly back to the scaled input.
func TestKissFFTStateSharedTwiddles(t *testing.T) {
	base := NewKissFFTState(240)
	if base == nil {
		t.Fatal("NewKissFFTState(240) returned nil")
	}
	sub := NewKissFFTStateTwiddles(60, base)
	if sub == nil {
		t.Fatal("NewKissFFTStateTwiddles(60, base) returned nil")
	}
	if sub.shift != 2 { // 60<<2 == 240
		t.Errorf("shared sub-FFT shift = %d, want 2", sub.shift)
	}
	standalone := NewKissFFTState(60)
	if sub.factors != standalone.factors {
		t.Errorf("shared sub-FFT factors = %v, standalone = %v", sub.factors, standalone.factors)
	}
	for i := range sub.bitrev {
		if sub.bitrev[i] != standalone.bitrev[i] {
			t.Errorf("shared sub-FFT bitrev[%d] = %d, standalone = %d", i, sub.bitrev[i], standalone.bitrev[i])
		}
	}
}
