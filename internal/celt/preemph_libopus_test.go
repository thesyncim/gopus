package celt

import (
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"math/rand"
	"testing"
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
	if count != len(cases) {
		return nil, fmt.Errorf("CELT preemphasis oracle returned %d cases, want %d", count, len(cases))
	}
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
	for i := range frames {
		tm := float64(i) * 0.17
		pcm[i*channels] = float32(0.42*math.Sin(tm+phase) + 0.09*math.Cos(0.31*tm))
		if channels == 2 {
			pcm[i*channels+1] = float32(0.35*math.Sin(0.73*tm+0.4) - 0.07*math.Cos(0.19*tm+phase))
		}
	}
	return pcm
}

func TestApplyPreemphasisUpsampledMatchesStuffedCore(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	negZero := float32(math.Copysign(0, -1))
	for trial := range 4000 {
		channels := 1 + rng.Intn(2)
		upsample := []int{2, 3, 4, 6}[rng.Intn(4)]
		apiN := []int{20, 40, 80, 160}[rng.Intn(4)] * 12 / upsample / 2
		frame := apiN * upsample
		overlap := min(120, frame)
		if rng.Intn(3) == 0 {
			overlap = rng.Intn(frame + 1)
		}
		native := make([]float32, channels*apiN)
		for i := range native {
			switch rng.Intn(6) {
			case 0:
			case 1:
				native[i] = negZero
			case 2:
				native[i] = float32(rng.NormFloat64() * 1e-6)
			default:
				native[i] = float32(rng.NormFloat64() * 0.3)
			}
		}
		stuffed := NewEncoder(channels)
		direct := NewEncoder(channels)
		coded := int32(1 + rng.Intn(channels))
		prev := float32(rng.NormFloat64() * 1e-5)
		for _, e := range []*Encoder{stuffed, direct} {
			e.upsample = int32(upsample)
			e.streamChannels = coded
			e.overlapMax = prev
		}
		for c := range channels {
			m := float32(rng.NormFloat64())
			if rng.Intn(4) == 0 {
				m = negZero
			}
			stuffed.preemphState[c], direct.preemphState[c] = m, m
		}
		wantOut := make([]float32, channels*(frame+overlap))
		gotOut := make([]float32, len(wantOut))
		core := stuffed.upsampleZeroStuff(native, apiN, channels, upsample)
		wantSilence := stuffed.applyPreemphasisWithScalingAndSilenceCore(core, wantOut, frame, overlap)
		gotSilence := direct.applyPreemphasisUpsampled(native, gotOut, frame, overlap)
		if gotSilence != wantSilence || math.Float32bits(direct.overlapMax) != math.Float32bits(stuffed.overlapMax) {
			t.Fatalf("trial %d: silence %t/%t overlap %08x/%08x", trial, gotSilence, wantSilence,
				math.Float32bits(direct.overlapMax), math.Float32bits(stuffed.overlapMax))
		}
		for c := range channels {
			if math.Float32bits(direct.preemphState[c]) != math.Float32bits(stuffed.preemphState[c]) {
				t.Fatalf("trial %d: channel %d state %v want %v", trial, c, direct.preemphState[c], stuffed.preemphState[c])
			}
		}
		for i := range wantOut {
			if math.Float32bits(gotOut[i]) != math.Float32bits(wantOut[i]) {
				t.Fatalf("trial %d: in[%d]=%v want %v", trial, i, gotOut[i], wantOut[i])
			}
		}
	}
}

// TestPreemphUpsampledPartialGroup covers an output length that ends inside a
// zero-stuffed group, against the single-tap loop on the stuffed input.
func TestPreemphUpsampledPartialGroup(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for trial := range 5000 {
		upsample := 2 + rng.Intn(5)
		step := 1 + rng.Intn(2)
		n := 1 + rng.Intn(40)
		groups := (n + upsample - 1) / upsample
		src := make([]float32, groups*step)
		for i := range src {
			src[i] = float32(rng.NormFloat64())
		}
		stuffed := make([]float32, n)
		for j := 0; j < n; j += upsample {
			stuffed[j] = src[(j/upsample)*step]
		}
		m0 := float32(rng.NormFloat64())
		want := make([]float32, n)
		wantM := preemphMonoScalar(stuffed, want, PreemphCoef, m0)
		got := make([]float32, n)
		gotM := preemphUpsampled(src, step, upsample, got, PreemphCoef, m0)
		if math.Float32bits(gotM) != math.Float32bits(wantM) {
			t.Fatalf("trial %d: m %v want %v", trial, gotM, wantM)
		}
		for i := range want {
			if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
				t.Fatalf("trial %d (upsample %d n %d): out[%d]=%v want %v", trial, upsample, n, i, got[i], want[i])
			}
		}
	}
}
