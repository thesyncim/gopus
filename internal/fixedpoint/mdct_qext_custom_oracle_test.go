//go:build gopus_fixed_point && gopus_qext && gopus_custom_modes

package fixedpoint

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQEXTCustomMDCTMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label: "custom fixed QEXT MDCT", OutputBase: "gopus_custom_fixed_qext_mdct",
		SourceFile:  "libopus_custom_fixed_qext_mdct.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "include"}, Libs: []string{"-lm"},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom fixed QEXT MDCT", err)
		return
	}
	for _, mode := range []struct{ fs, frame int }{
		{32000, 640}, {24000, 480}, {16000, 320}, {12000, 240}, {8000, 160},
		{48000, 64}, {48000, 640}, {48000, 512}, {48000, 600},
		{96000, 1024}, {96000, 600}, {96000, 1440}, {96000, 2048},
	} {
		t.Run(fmt.Sprintf("fs%d_n%d", mode.fs, mode.frame), func(t *testing.T) {
			input := mdctSignal(2*mode.frame, int64(mode.fs+mode.frame))
			payload := libopustest.NewOraclePayload("GCQI", uint32(mode.fs), uint32(mode.frame))
			payload.I32s(input...)
			r, err := libopustest.RunOracle(helper, payload.Bytes(), "custom fixed QEXT MDCT", "GCQO")
			if err != nil {
				t.Fatal(err)
			}
			n, maxshift, overlap := int(r.U32()), int(r.U32()), int(r.U32())
			l := NewQEXTMDCTLookup(n, maxshift, overlap)
			if l == nil {
				t.Fatalf("unsupported C geometry %d/%d/%d", n, maxshift, overlap)
			}
			check := func(label string, i int, got int32) {
				t.Helper()
				if want := r.I32(); got != want {
					t.Fatalf("%s[%d]: Go=%d C=%d", label, i, got, want)
				}
			}
			for i, v := range l.trig {
				check("trig", i, v)
			}
			for i, v := range l.window {
				check("window", i, v)
			}
			for shift, st := range l.kfft {
				for i, v := range []int32{int32(st.nfft), st.scale, int32(st.scaleShift), int32(st.shift)} {
					check(fmt.Sprintf("fft%d metadata", shift), i, v)
				}
				stages := int(r.U32())
				if stages < 1 || stages > maxFactors || st.factors[2*stages-1] != 1 {
					t.Fatalf("invalid C FFT factor count %d", stages)
				}
				for i, v := range st.factors[:2*stages] {
					check("factors", i, int32(v))
				}
				for i, v := range st.bitrev {
					check("bitrev", i, int32(v))
				}
			}
			for i, v := range l.kfft[0].twiddles {
				check("twiddle real", i, v.R)
				check("twiddle imag", i, v.I)
			}
			var scratch QEXTMDCTScratch
			out, back := make([]int32, n), make([]int32, n)
			for shift := range maxshift + 1 {
				count := n >> (shift + 1)
				for _, stride := range []int{1, 1 << shift} {
					clear(out)
					l.MDCTForward(input, out, nil, overlap, shift, stride, &scratch)
					for i, v := range out[:stride*(count-1)+1] {
						check(fmt.Sprintf("forward shift%d stride%d", shift, stride), i, v)
					}
					copy(back, input)
					l.MDCTBackward(out, back, nil, overlap, shift, stride, &scratch)
					for i, v := range back[:count+overlap/2] {
						check(fmt.Sprintf("backward shift%d stride%d", shift, stride), i, v)
					}
				}
			}
			if err := r.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
			if got := testing.AllocsPerRun(10, func() {
				l.MDCTForward(input, out, nil, overlap, 0, 1, &scratch)
				l.MDCTBackward(out, back, nil, overlap, 0, 1, &scratch)
			}); got != 0 {
				t.Fatalf("warm transforms allocate %g times", got)
			}
		})
	}
}
