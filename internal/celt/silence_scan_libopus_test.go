package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestCELTSilenceScanMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	type scanCase struct {
		name                            string
		channels, coded, frame, overlap int
		upsample                        int
		previous                        float32
		nonzeroIndex                    int
		amplitude                       float32
		twoTap                          bool
	}
	cases := []scanCase{
		{"stereo_mono_coded_tail_outside_scan", 2, 1, 120, 120, 1, 0, 130, .5, false},
		{"prior_overlap_prevents_silence", 2, 1, 120, 120, 1, .375, -1, 0, false},
		{"stereo_coded_tail_inside_scan", 2, 2, 120, 120, 1, 0, 130, .5, false},
		{"8k_stereo_mono_coded", 2, 1, 120, 120, 6, 0, 27, -.5, false},
		{"8k_inside_coded_prefix", 2, 1, 120, 120, 6, 0, 17, -.5, false},
		{"12k_stereo_mono_coded", 2, 1, 120, 120, 4, 0, 41, .5, false},
		{"12k_inside_coded_prefix", 2, 1, 120, 120, 4, 0, 27, .5, false},
		{"24k_stereo_mono_coded", 2, 1, 120, 120, 2, 0, 79, -.5, false},
		{"24k_inside_coded_prefix", 2, 1, 120, 120, 2, 0, 57, -.5, false},
		{"5ms_first_and_overlap", 2, 1, 240, 120, 1, 0, 130, .7, false},
		{"24k_5ms_first_region", 2, 1, 240, 120, 2, 0, 57, .7, false},
		{"24k_5ms_overlap_region", 2, 1, 240, 120, 2, 0, 117, .7, false},
		{"negative_zero_overlap", 2, 1, 120, 120, 1, 0, 119, math.Float32frombits(0x80000000), false},
		{"nan_overlap", 2, 1, 120, 120, 1, 0, 119, math.Float32frombits(0x7fc12345), false},
		{"hd_two_tap_overlap_state", 2, 1, 960, 240, 1, 0, 300, .25, true},
		{"hd_two_tap_prior_overlap", 2, 1, 960, 240, 1, .375, -1, 0, true},
	}
	payload := libopustest.NewOraclePayloadVersion("GSSI", 1, uint32(len(cases)))
	for _, tc := range cases {
		native := make([]float32, tc.channels*tc.frame/tc.upsample)
		if tc.nonzeroIndex >= 0 {
			native[tc.nonzeroIndex] = tc.amplitude
		}
		payload.U32(uint32(tc.channels))
		payload.U32(uint32(tc.coded))
		payload.U32(uint32(tc.frame))
		payload.U32(uint32(tc.overlap))
		payload.U32(uint32(tc.upsample))
		payload.U32(math.Float32bits(tc.previous))
		payload.U32(uint32(len(native)))
		payload.Float32s(native...)
	}
	bin, err := libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "CELT raw silence scan",
		OutputBase:  "gopus_libopus_celt_silence_scan",
		SourceFile:  "libopus_celt_silence_scan_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		t.Fatalf("build live C CELT silence helper: %v", err)
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "CELT raw silence scan", "GSSO")
	if err != nil {
		t.Fatalf("run live C CELT silence helper: %v", err)
	}
	if got := reader.Count(len(cases)); got != len(cases) {
		t.Fatalf("live C case count=%d want=%d", got, len(cases))
	}
	for _, tc := range cases {
		wantSilence := reader.U32() != 0
		_ = reader.U32() // C's full sample_max; the state and decision are asserted below.
		wantOverlap := reader.U32()
		t.Run(tc.name, func(t *testing.T) {
			native := make([]float32, tc.channels*tc.frame/tc.upsample)
			if tc.nonzeroIndex >= 0 {
				native[tc.nonzeroIndex] = tc.amplitude
			}
			core := make([]float32, tc.channels*tc.frame)
			for i, sample := range native {
				core[(i/tc.channels)*tc.upsample*tc.channels+i%tc.channels] = sample
			}
			output := make([]float32, tc.channels*(tc.frame+tc.overlap))
			enc := NewEncoder(tc.channels)
			enc.streamChannels = int32(tc.coded)
			enc.upsample = int32(tc.upsample)
			enc.overlapMax = tc.previous
			if tc.twoTap {
				enc.hd96kPreemph = [4]float32{.85, .3, .45, 0}
			}
			gotSilence := enc.applyPreemphasisWithScalingAndSilenceCore(core, output, tc.frame, tc.overlap)
			if gotSilence != wantSilence || math.Float32bits(enc.overlapMax) != wantOverlap {
				t.Fatalf("silence Go/C=%t/%t overlap bits Go/C=%08x/%08x",
					gotSilence, wantSilence, math.Float32bits(enc.overlapMax), wantOverlap)
			}
			// The caller supplies input and output buffers; the steady-state
			// scan and pre-emphasis path must allocate nothing.
			enc.overlapMax = tc.previous
			enc.applyPreemphasisWithScalingAndSilenceCore(core, output, tc.frame, tc.overlap)
			if allocs := testing.AllocsPerRun(50, func() {
				enc.overlapMax = tc.previous
				enc.applyPreemphasisWithScalingAndSilenceCore(core, output, tc.frame, tc.overlap)
			}); allocs != 0 {
				t.Fatalf("warm allocations=%g want 0", allocs)
			}
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
