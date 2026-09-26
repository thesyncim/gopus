//go:build gopus_custom_modes

package celt

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

var customMDCTTablesHelper libopustest.HelperCache

func TestCustomModeTransformsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := customMDCTTablesHelper.Path(func() (string, error) {
		return libopustest.BuildCHelper(libopustest.CHelperConfig{
			Label: "custom mode transforms", OutputBase: "gopus_custom_mdct_tables", SourceFile: "libopus_custom_mdct_tables.c",
			CFlags: []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"}, RefIncludes: []string{"celt"}, CustomRef: true,
			Libs: []string{libopustest.CustomRefPath(".libs", "libopus.a"), "-lm"}, DeadStrip: true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom mode transforms", err)
		return
	}
	for _, tc := range []struct{ fs, frame, maxLM int }{{24000, 40, 0}, {12000, 240, 3}, {44100, 360, 2}, {48000, 640, 3}, {48000, 720, 3}, {48000, 1024, 3}} {
		t.Run(fmt.Sprintf("%d_%d", tc.fs, tc.frame), func(t *testing.T) {
			overlap := ((tc.frame >> tc.maxLM) >> 2) << 2
			window := GetWindowBufferF32(overlap)
			plan := NewCustomMDCTTables(tc.frame, tc.maxLM, window)
			if plan == nil {
				t.Fatal("dynamic transform allocation failed")
			}
			request := libopustest.NewOraclePayload("GMTI", uint32(tc.fs), uint32(tc.frame))
			var inputs, histories, spectra [4][]float32
			for shift := 0; shift <= tc.maxLM; shift++ {
				block := tc.frame >> shift
				inputs[shift] = make([]float32, block+overlap)
				histories[shift] = make([]float32, overlap)
				spectra[shift] = make([]float32, block)
				for kind, values := range [][]float32{inputs[shift], histories[shift], spectra[shift]} {
					for i := range values {
						values[i] = float32(((i+3)*(kind+7)+shift*11)%97-48) * (1.0 / 16)
						request.Float32(values[i])
					}
				}
			}
			reader, err := libopustest.RunOracle(helper, request.Bytes(), "custom mode transforms", "GMTO")
			if err != nil {
				t.Fatal(err)
			}
			gotOverlap, gotLM := reader.U32(), reader.U32()
			if gotOverlap != uint32(overlap) || gotLM != uint32(tc.maxLM) {
				t.Fatalf("C geometry overlap/LM=%d/%d want=%d/%d", gotOverlap, gotLM, overlap, tc.maxLM)
			}
			compare := func(label string, got []float32) {
				t.Helper()
				for i, v := range got {
					want := reader.U32()
					if math.Float32bits(v) != want {
						t.Errorf("%s[%d]=%08x want=%08x", label, i, math.Float32bits(v), want)
					}
				}
			}
			compare("window", window)
			for shift := 0; shift <= tc.maxLM; shift++ {
				block := tc.frame >> shift
				tables := &plan.blocks[shift]
				fft := tables.fft
				if n := reader.U32(); n != uint32(fft.nfft) {
					t.Fatalf("shift%d C FFT size=%d want=%d", shift, n, fft.nfft)
				}
				for i := range fft.nfft {
					v := fft.w[i<<max(0, fft.shift)]
					compare(fmt.Sprintf("shift%d/twiddle%d", shift, i), []float32{v.r, v.i})
				}
				compare(fmt.Sprintf("shift%d/trig", shift), tables.trig)
				var forward encoderScratch
				forward.customTransforms = plan
				var inverse imdctScratchF32
				inverse.customTransforms = plan
				coeffs := make([]float32, block)
				runForward := func() {
					mdctForwardOverlapF32Scratch(inputs[shift], overlap, coeffs, forward.mdctF, forward.mdctFFTIn, forward.mdctFFTOut, forward.mdctFFTTmp, forward.mdctLookup(2*block))
				}
				forward.mdctFFTTmp = make([]kissCpx, block/2)
				runForward()
				compare(fmt.Sprintf("shift%d/forward", shift), coeffs)
				decoded := imdctOverlapWithPrevScratchF32Output32(spectra[shift], histories[shift], overlap, &inverse)
				compare(fmt.Sprintf("shift%d/inverse", shift), decoded)
				if allocs := testing.AllocsPerRun(50, func() {
					runForward()
					imdctOverlapWithPrevScratchF32Output32(spectra[shift], histories[shift], overlap, &inverse)
				}); allocs != 0 {
					t.Errorf("shift%d warm transform allocations=%g want=0", shift, allocs)
				}
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
