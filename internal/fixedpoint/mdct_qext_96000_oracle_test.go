//go:build gopus_fixed_point && gopus_qext

package fixedpoint

import (
	"math/rand"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var qextMDCTAllocSink int32

func TestQEXTMDCT96000ForwardMatchesFixedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	lookup := NewStaticQEXTMDCTLookup96000()
	if lookup == nil || lookup.N() != 3840 {
		t.Fatal("missing 96 kHz fixed-QEXT CELT MDCT lookup")
	}
	rng := rand.New(rand.NewSource(0x514558543936))
	for shift := 0; shift <= 3; shift++ {
		t.Run("shift="+string(rune('0'+shift)), func(t *testing.T) {
			stride := 1 << shift
			input := make([]int32, lookup.N())
			for i := range input {
				input[i] = int32(rng.Intn(1<<23) - (1 << 22))
			}
			want, err := libopustest.ProbeCELTFixedQEXTMDCT(libopustest.CELTFixedQEXTMDCTParams{
				Mode: 1, Shift: shift, Stride: stride, Input: input,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT CELT 96 kHz MDCT", err)
				return
			}
			out := make([]int32, len(want.Output))
			capture := &qextMDCTStageCapture{
				fold:   make([]int32, len(want.Fold)),
				preFFT: make([]FFTCpx, len(want.PreFFT)/2),
				fft:    make([]FFTCpx, len(want.Fold)>>1),
			}
			lookup.mdctForward(append([]int32(nil), input...), out, nil, 240, shift, stride, &QEXTMDCTScratch{}, capture)
			compareQEXTMDCTStages(t, shift, capture, out, want, lookup)
		})
	}
}

func TestQEXTMDCTSilenceHeadroomMatchesFixedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, mode := range []struct {
		id     int
		lookup *QEXTMDCTLookup
	}{
		{id: 0, lookup: NewStaticQEXTMDCTLookup48000()},
		{id: 1, lookup: NewStaticQEXTMDCTLookup96000()},
	} {
		for shift := 0; shift <= 3; shift++ {
			t.Run("mode="+string(rune('0'+mode.id))+"/shift="+string(rune('0'+shift)), func(t *testing.T) {
				stride := 1 << shift
				input := make([]int32, mode.lookup.N())
				want, err := libopustest.ProbeCELTFixedQEXTMDCT(libopustest.CELTFixedQEXTMDCTParams{
					Mode: mode.id, Shift: shift, Stride: stride, Input: input,
				})
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed-QEXT CELT MDCT silence", err)
					return
				}
				out := make([]int32, len(want.Output))
				capture := &qextMDCTStageCapture{
					fold:   make([]int32, len(want.Fold)),
					preFFT: make([]FFTCpx, len(want.PreFFT)/2),
					fft:    make([]FFTCpx, len(want.Fold)>>1),
				}
				mode.lookup.mdctForward(input, out, nil, len(mode.lookup.window), shift, stride, &QEXTMDCTScratch{}, capture)
				if capture.headroom != want.Headroom {
					t.Fatalf("headroom=%d, libopus=%d", capture.headroom, want.Headroom)
				}
				for i := range out {
					if out[i] != want.Output[i] {
						t.Fatalf("output[%d]=%d, libopus=%d", i, out[i], want.Output[i])
					}
				}
			})
		}
	}
}

func TestQEXTMDCTForwardDoesNotAllocateAfterWarmup(t *testing.T) {
	for _, lookup := range []*QEXTMDCTLookup{
		NewStaticQEXTMDCTLookup48000(),
		NewStaticQEXTMDCTLookup96000(),
	} {
		input := make([]int32, lookup.N())
		output := make([]int32, lookup.N()/2)
		scratch := &QEXTMDCTScratch{}
		lookup.MDCTForward(input, output, nil, len(lookup.window), 0, 1, scratch)
		if got := testing.AllocsPerRun(100, func() {
			lookup.MDCTForward(input, output, nil, len(lookup.window), 0, 1, scratch)
			qextMDCTAllocSink = output[0]
		}); got != 0 {
			t.Fatalf("N=%d warm MDCT allocations/run=%g, want 0", lookup.N(), got)
		}
	}
}

func TestQEXTKissFFTDoesNotAllocate(t *testing.T) {
	for _, lookup := range []*QEXTMDCTLookup{
		NewStaticQEXTMDCTLookup48000(),
		NewStaticQEXTMDCTLookup96000(),
	} {
		state := lookup.kfft[0]
		input := make([]FFTCpx, state.Nfft())
		output := make([]FFTCpx, state.Nfft())
		state.OpusFFT(input, output)
		if got := testing.AllocsPerRun(100, func() {
			state.OpusFFT(input, output)
			qextMDCTAllocSink = output[0].R
		}); got != 0 {
			t.Fatalf("nfft=%d warm FFT allocations/run=%g, want 0", state.Nfft(), got)
		}
	}
}

func compareQEXTMDCTStages(t *testing.T, shift int, got *qextMDCTStageCapture, output []int32, want libopustest.CELTFixedQEXTMDCTRecord, lookup *QEXTMDCTLookup) {
	t.Helper()
	for i := range got.fold {
		if got.fold[i] != want.Fold[i] {
			t.Fatalf("fold[%d]=%d, libopus=%d", i, got.fold[i], want.Fold[i])
		}
	}
	for i := range got.preFFT {
		if got.preFFT[i].R != want.PreFFT[2*i] || got.preFFT[i].I != want.PreFFT[2*i+1] {
			t.Fatalf("preFFT[%d]={%d,%d}, libopus={%d,%d}", i, got.preFFT[i].R, got.preFFT[i].I, want.PreFFT[2*i], want.PreFFT[2*i+1])
		}
	}
	if got.headroom != want.Headroom {
		t.Fatalf("headroom=%d, libopus=%d", got.headroom, want.Headroom)
	}
	for i := range got.fft {
		if got.fft[i].R != want.FFT[2*i] || got.fft[i].I != want.FFT[2*i+1] {
			t.Fatalf("fft[%d]={%d,%d}, libopus={%d,%d}", i, got.fft[i].R, got.fft[i].I, want.FFT[2*i], want.FFT[2*i+1])
		}
	}
	trig, _ := lookup.trigForShift(shift)
	for i := range got.fft {
		t0 := qextMul(trig[i], lookup.kfft[shift].scale)
		t1 := qextMul(trig[len(got.fft)+i], lookup.kfft[shift].scale)
		terms := [6]int32{t0, t1, qextMul(got.fft[i].I, t1), qextMul(got.fft[i].R, t0), qextMul(got.fft[i].R, t1), qextMul(got.fft[i].I, t0)}
		for j, term := range terms {
			if term != want.Post[6*i+j] {
				t.Fatalf("post term[%d][%d]=%d, libopus=%d", i, j, term, want.Post[6*i+j])
			}
		}
	}
	for i := range output {
		if output[i] != want.Output[i] {
			t.Fatalf("output[%d]=%d, libopus=%d", i, output[i], want.Output[i])
		}
	}
}
