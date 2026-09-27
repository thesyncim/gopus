//go:build gopus_osce

package lpcnetplc

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var conv3x3AllocSink float32

func TestPitchDNNConvolutionStagesMatchSelectedLibopus(t *testing.T) {
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
			for _, stage := range []struct {
				name string
				got  []float32
				want []float32
			}{
				{"conv1", goNet.scratch.conv1Tmp2[1 : 1+pitchXcorrFeatures], want.Conv1Out},
				{"conv2", goNet.scratch.downsampler[:pitchXcorrFeatures], want.Conv2Out},
			} {
				if len(stage.got) != len(stage.want) {
					t.Fatalf("%s length=%d want %d", stage.name, len(stage.got), len(stage.want))
				}
				for i := range stage.got {
					gotBits := math.Float32bits(stage.got[i])
					wantBits := math.Float32bits(stage.want[i])
					if gotBits != wantBits {
						t.Fatalf("%s[%d]=0x%08x want 0x%08x", stage.name, i, gotBits, wantBits)
					}
				}
			}
		})
	}
}

func TestPitchDNNConv3x3WarmZeroAlloc(t *testing.T) {
	var operands [18]float32
	for i := range operands {
		operands[i] = float32((i%7)-3) / float32(i+9)
	}
	call := func() {
		conv3x3AllocSink = conv3x3Acc9(
			operands[0], operands[1], operands[2], operands[3], operands[4], operands[5], operands[6], operands[7], operands[8],
			operands[9], operands[10], operands[11], operands[12], operands[13], operands[14], operands[15], operands[16], operands[17])
	}
	call()
	if allocs := testing.AllocsPerRun(1000, call); allocs != 0 {
		t.Fatalf("conv3x3Acc9 warm allocations=%g want 0", allocs)
	}
}
