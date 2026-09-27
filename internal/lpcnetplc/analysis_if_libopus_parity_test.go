//go:build gopus_osce

package lpcnetplc

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestLPCNetInstantaneousFrequencyMatchesSelectedLibopusRawBits(t *testing.T) {
	libopustest.RequireOracle(t)
	rawModel, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Fatalf("selected libopus PitchDNN model: %v", err)
	}
	model, err := dnnblob.Clone(rawModel)
	if err != nil {
		t.Fatalf("clone PitchDNN model: %v", err)
	}
	frames := dredParityAnalysisFrames(7, 1920)
	longCount := len(frames) / FrameSize
	for _, tc := range []struct {
		name       string
		frameCount int
	}{
		{"cold", 1},
		{"two_frames", 2},
		{"long_state", longCount},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pcm := frames[:tc.frameCount*FrameSize]
			want, err := probeLibopusLPCNetFeatures(pcm)
			if err != nil {
				t.Fatalf("selected libopus analysis: %v", err)
			}
			if len(want.IFFeatures) != pitchIFFeatures {
				t.Fatalf("selected libopus IF count=%d want %d", len(want.IFFeatures), pitchIFFeatures)
			}
			var got Analysis
			got.SetDREDEncoderMode(true)
			if err := got.SetModel(model); err != nil {
				t.Fatalf("Analysis.SetModel: %v", err)
			}
			var features [NumTotalFeatures]float32
			for frame := 0; frame < tc.frameCount; frame++ {
				input := pcm[frame*FrameSize : (frame+1)*FrameSize]
				if n := got.ComputeSingleFrameFeaturesFloat(features[:], input); n != NumTotalFeatures {
					t.Fatalf("frame %d features=%d want %d", frame, n, NumTotalFeatures)
				}
			}
			for i, c := range want.IFFeatures {
				if gb, cb := math.Float32bits(got.ifFeatures[i]), math.Float32bits(c); gb != cb {
					t.Errorf("IF[%d] Go=%08x C=%08x", i, gb, cb)
				}
			}
		})
	}
}
