//go:build gopus_osce

package lpcnetplc

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPitchDNNGRUStateMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Fatalf("PitchDNN model oracle: %v", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatal(err)
	}
	var analysis Analysis
	analysis.SetDREDEncoderMode(true)
	if err := analysis.SetModel(blob); err != nil {
		t.Fatal(err)
	}
	frames := dredParityAnalysisFrames(4, 1920)
	for frame := 0; frame < len(frames)/FrameSize; frame++ {
		t.Run(fmt.Sprintf("frame_%02d", frame), func(t *testing.T) {
			preState := analysis.pitch.state
			var features [NumTotalFeatures]float32
			analysis.ComputeSingleFrameFeaturesFloat(features[:], frames[frame*FrameSize:(frame+1)*FrameSize])

			var goNet PitchDNN
			goNet.model = analysis.pitch.model
			goNet.state = preState
			goNet.Compute(analysis.ifFeatures[:], analysis.xcorrFeatures[:])
			want, err := probeLibopusPitchDNNStages(analysis.ifFeatures[:], analysis.xcorrFeatures[:], preState)
			if err != nil {
				t.Fatalf("selected libopus PitchDNN stages: %v", err)
			}
			if len(want.GRUState) != len(goNet.state.gruState) {
				t.Fatalf("GRU state length=%d want %d", len(goNet.state.gruState), len(want.GRUState))
			}
			for i, got := range goNet.state.gruState {
				if gotBits, wantBits := math.Float32bits(got), math.Float32bits(want.GRUState[i]); gotBits != wantBits {
					t.Fatalf("GRU state[%d]=0x%08x want 0x%08x", i, gotBits, wantBits)
				}
			}
		})
	}
}
