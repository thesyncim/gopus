package multistream

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestProjectionShortMixingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	bin, err := libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "projection short mixing",
		OutputBase:  "gopus_libopus_projection_mix_short",
		SourceFile:  "libopus_projection_mix_short_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "src"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		t.Fatalf("build live C short matrix helper: %v", err)
	}
	for _, tc := range []struct {
		name                string
		channels, frameSize int
	}{
		{"foa_5ms", 4, 240},
		{"foa_60ms", 4, 2880},
		{"soa_20ms", 9, 960},
		{"soa_60ms", 9, 2880},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := NewProjectionEncoder(48000, tc.channels)
			if err != nil {
				t.Fatal(err)
			}
			if enc.projectionRows != tc.channels || enc.projectionCols != tc.channels {
				t.Fatalf("projection dimensions=%d/%d want=%d", enc.projectionRows, enc.projectionCols, tc.channels)
			}
			input := make([]int16, tc.frameSize*tc.channels)
			for i := range input {
				input[i] = int16((i*7919+tc.channels*3137)%65536 - 32768)
			}
			payload := libopustest.NewOraclePayloadVersion("GMSI", 1,
				uint32(tc.channels), uint32(tc.channels), uint32(tc.frameSize))
			for _, coeff := range enc.projectionMixing {
				payload.I16(coeff)
			}
			for _, sample := range input {
				payload.I16(sample)
			}
			reader, err := libopustest.RunOracle(bin, payload.Bytes(), "projection short mixing", "GMSO")
			if err != nil {
				t.Fatalf("run live C short matrix helper: %v", err)
			}
			if got := reader.Count(len(input)); got != len(input) {
				t.Fatalf("C output count=%d want=%d", got, len(input))
			}
			buffers := enc.routeProjectionMixingShortToStreams(nil, input, tc.frameSize)
			for sample := range tc.frameSize {
				for row := range tc.channels {
					mappingIdx := enc.mapping[row]
					streamIdx, chanInStream := resolveMapping(mappingIdx, enc.coupledStreams)
					channels := streamChannels(streamIdx, enc.coupledStreams)
					got := math.Float32bits(buffers[streamIdx][sample*channels+chanInStream])
					want := reader.U32()
					if got != want {
						t.Fatalf("sample=%d outputRow=%d stream=%d/%d bits Go/C=%08x/%08x", sample, row, streamIdx, chanInStream, got, want)
					}
				}
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
			enc.streamInputScratch = buffers
			enc.routeProjectionMixingShortToStreams(enc.streamInputScratch, input, tc.frameSize)
			if allocs := testing.AllocsPerRun(50, func() {
				enc.streamInputScratch = enc.routeProjectionMixingShortToStreams(enc.streamInputScratch, input, tc.frameSize)
			}); allocs != 0 {
				t.Fatalf("warm short matrix allocations=%g want 0", allocs)
			}
		})
	}
}
