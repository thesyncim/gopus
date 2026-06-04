package celt

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

type libopusCELTPreemphasisCase struct {
	name     string
	channels int
	pcm      []float32
	mem      []float32
}

type libopusCELTPreemphasisResult struct {
	out []float32
	mem []float32
}

var libopusCELTPreemphasisHelper libopustest.HelperCache

func getLibopusCELTPreemphasisHelperPath() (string, error) {
	return libopusCELTPreemphasisHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "celt preemphasis",
		OutputBase:  "gopus_libopus_celt_preemphasis",
		SourceFile:  "libopus_celt_preemphasis_info.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
		RefIncludes: []string{"celt", "silk", "src"},
		Libs:        []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func probeLibopusCELTPreemphasis(cases []libopusCELTPreemphasisCase) ([]libopusCELTPreemphasisResult, error) {
	binPath, err := getLibopusCELTPreemphasisHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GCPI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(tc.channels))
		payload.U32(uint32(len(tc.pcm) / tc.channels))
		for ch := 0; ch < tc.channels; ch++ {
			payload.Float32(tc.mem[ch])
		}
		for _, sample := range tc.pcm {
			payload.Float32(sample)
		}
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "celt preemphasis", "GCPO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	totalFloats := 0
	for _, tc := range cases {
		totalFloats += len(tc.pcm) + tc.channels
	}
	reader.ExpectRemaining(4 * totalFloats)
	out := make([]libopusCELTPreemphasisResult, count)
	for i, tc := range cases {
		out[i].out = make([]float32, len(tc.pcm))
		for j := range out[i].out {
			out[i].out[j] = reader.Float32()
		}
		out[i].mem = make([]float32, tc.channels)
		for ch := range out[i].mem {
			out[i].mem[ch] = reader.Float32()
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestApplyPreemphasisWithScalingMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []libopusCELTPreemphasisCase{
		{
			name:     "mono_ramp",
			channels: 1,
			mem:      []float32{123.25},
			pcm:      preemphOraclePCM(1, 16, 0.13),
		},
		{
			name:     "stereo_asymmetric",
			channels: 2,
			mem:      []float32{-17.5, 29.75},
			pcm:      preemphOraclePCM(2, 18, 0.37),
		},
		{
			name:     "stereo_edges",
			channels: 2,
			mem:      []float32{0, -0},
			pcm: []float32{
				0, -0, 1.0 / 32768.0, -1.0 / 32768.0,
				0.9999695, -0.9999695, 0.25, -0.125,
			},
		},
	}
	want, err := probeLibopusCELTPreemphasis(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "celt preemphasis", err)
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotOut, gotMem := runGoCELTPreemphasisWithScaling(tc)
			for j := range gotOut {
				if math.Float32bits(gotOut[j]) != math.Float32bits(want[i].out[j]) {
					t.Fatalf("out[%d]=%08x %.9g want %08x %.9g",
						j, math.Float32bits(gotOut[j]), gotOut[j],
						math.Float32bits(want[i].out[j]), want[i].out[j])
				}
			}
			for ch := range gotMem {
				if math.Float32bits(gotMem[ch]) != math.Float32bits(want[i].mem[ch]) {
					t.Fatalf("mem[%d]=%08x %.9g want %08x %.9g",
						ch, math.Float32bits(gotMem[ch]), gotMem[ch],
						math.Float32bits(want[i].mem[ch]), want[i].mem[ch])
				}
			}
		})
	}
}

func runGoCELTPreemphasisWithScaling(tc libopusCELTPreemphasisCase) ([]float32, []float32) {
	enc := NewEncoder(tc.channels)
	for ch := 0; ch < tc.channels; ch++ {
		enc.preemphState[ch] = celtSig(tc.mem[ch])
	}
	in := make([]float32, len(tc.pcm))
	copy(in, tc.pcm)
	out := make([]float32, len(in))
	enc.applyPreemphasisWithScalingCore(in, out)
	got := make([]float32, len(out))
	copy(got, out)
	mem := append([]float32(nil), enc.PreemphState()...)
	return got, mem
}

func preemphOraclePCM(channels, frames int, phase float64) []float32 {
	pcm := make([]float32, frames*channels)
	for i := 0; i < frames; i++ {
		tm := float64(i) * 0.17
		pcm[i*channels] = float32(0.42*math.Sin(tm+phase) + 0.09*math.Cos(0.31*tm))
		if channels == 2 {
			pcm[i*channels+1] = float32(0.35*math.Sin(0.73*tm+0.4) - 0.07*math.Cos(0.19*tm+phase))
		}
	}
	return pcm
}
