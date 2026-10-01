//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQEXTMDCTForwardMatchesFixedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	lookup := NewStaticQEXTMDCTLookup48000()
	if lookup == nil || lookup.N() != 1920 {
		t.Fatal("missing 48 kHz fixed-QEXT CELT MDCT lookup")
	}
	rng := rand.New(rand.NewSource(0x51455854))
	for shift := 0; shift <= 3; shift++ {
		t.Run("shift="+string(rune('0'+shift)), func(t *testing.T) {
			stride := 1 << shift
			input := make([]int32, lookup.N())
			for i := range input {
				input[i] = int32(rng.Intn(1<<23) - (1 << 22))
			}
			want, err := libopustest.ProbeCELTFixedQEXTMDCT(libopustest.CELTFixedQEXTMDCTParams{
				Mode: 0, Shift: shift, Stride: stride, Input: input,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT CELT MDCT", err)
				return
			}
			out := make([]int32, len(want.Output))
			capture := &qextMDCTStageCapture{
				fold:   make([]int32, len(want.Fold)),
				preFFT: make([]FFTCpx, len(want.PreFFT)/2),
				fft:    make([]FFTCpx, (len(want.Fold) >> 1)),
			}
			lookup.mdctForward(append([]int32(nil), input...), out, nil, 120, shift, stride, &QEXTMDCTScratch{}, capture)
			for i := range capture.fold {
				if capture.fold[i] != want.Fold[i] {
					t.Fatalf("shift=%d stride=%d fold[%d]=%d, libopus=%d", shift, stride, i, capture.fold[i], want.Fold[i])
				}
			}
			for i := range capture.preFFT {
				if capture.preFFT[i].R != want.PreFFT[2*i] || capture.preFFT[i].I != want.PreFFT[2*i+1] {
					t.Fatalf("shift=%d stride=%d preFFT[%d]={%d,%d}, libopus={%d,%d}",
						shift, stride, i, capture.preFFT[i].R, capture.preFFT[i].I, want.PreFFT[2*i], want.PreFFT[2*i+1])
				}
			}
			if capture.headroom != want.Headroom {
				t.Fatalf("shift=%d headroom=%d, libopus=%d", shift, capture.headroom, want.Headroom)
			}
			for i := range capture.fft {
				if capture.fft[i].R != want.FFT[2*i] || capture.fft[i].I != want.FFT[2*i+1] {
					t.Fatalf("shift=%d stride=%d fft[%d]={%d,%d}, libopus={%d,%d}",
						shift, stride, i, capture.fft[i].R, capture.fft[i].I, want.FFT[2*i], want.FFT[2*i+1])
				}
			}
			trig, _ := lookup.trigForShift(shift)
			for i := range capture.fft {
				t0 := qextMul(trig[i], lookup.kfft[shift].scale)
				t1 := qextMul(trig[len(capture.fft)+i], lookup.kfft[shift].scale)
				terms := [6]int32{
					t0,
					t1,
					qextMul(capture.fft[i].I, t1),
					qextMul(capture.fft[i].R, t0),
					qextMul(capture.fft[i].R, t1),
					qextMul(capture.fft[i].I, t0),
				}
				for j, got := range terms {
					if got != want.Post[6*i+j] {
						t.Fatalf("shift=%d post term[%d][%d]=%d, libopus=%d", shift, i, j, got, want.Post[6*i+j])
					}
				}
			}
			for i := range out {
				if out[i] != want.Output[i] {
					if i == 0 {
						a, b := want.Post[2], want.Post[3]
						t.Logf("headroom=%d post=(%d-%d), Go rounded=%d", want.Headroom, a, b, pshr32(a-b, want.Headroom))
					}
					t.Fatalf("shift=%d stride=%d output[%d]=%d, libopus=%d", shift, stride, i, out[i], want.Output[i])
				}
			}
		})
	}
}

func TestQEXTKissFFTMatchesFixedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	st := StaticQEXTCELT48000FFTState()
	input := make([]int32, 2*st.Nfft())
	rng := rand.New(rand.NewSource(0x51465854))
	for i := range input {
		input[i] = int32(rng.Intn(1<<22) - (1 << 21))
	}
	want, err := libopustest.ProbeCELTFixedQEXTFFT(libopustest.CELTFixedQEXTFFTParams{Input: input})
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed-QEXT CELT FFT", err)
		return
	}
	fin := make([]FFTCpx, st.Nfft())
	fout := make([]FFTCpx, st.Nfft())
	for i := range fin {
		fin[i] = FFTCpx{R: input[2*i], I: input[2*i+1]}
	}
	st.OpusFFT(fin, fout)
	for i := range fout {
		if fout[i].R != want[2*i] || fout[i].I != want[2*i+1] {
			t.Fatalf("FFT[%d]={%d,%d}, libopus={%d,%d}", i, fout[i].R, fout[i].I, want[2*i], want[2*i+1])
		}
	}
}
