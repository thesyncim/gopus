//go:build gopus_osce && arm64

package lpcnetplc

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestLPCNetPitchInputsMatchSelectedLibopus keeps the selected ARM scalar or
// NEON correlation and inner-product path exact over stateful DRED input runs.
func TestLPCNetPitchInputsMatchSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "pitchdnn model", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("clone selected pitch model: %v", err)
	}

	for _, frameSize := range []int{1920, 2880} {
		t.Run(fmt.Sprintf("%d_samples", frameSize), func(t *testing.T) {
			var analysis Analysis
			analysis.SetDREDEncoderMode(true)
			if err := analysis.SetModel(blob); err != nil {
				t.Fatalf("set selected pitch model: %v", err)
			}
			frames := dredParityAnalysisFrames(4, frameSize)
			frameCount := len(frames) / FrameSize
			for frame := 0; frame < frameCount; frame++ {
				t.Run(fmt.Sprintf("frame_%02d", frame), func(t *testing.T) {
					var got [NumTotalFeatures]float32
					input := frames[frame*FrameSize : (frame+1)*FrameSize]
					if n := analysis.ComputeSingleFrameFeaturesFloat(got[:], input); n != NumTotalFeatures {
						t.Fatalf("compute features=%d want %d", n, NumTotalFeatures)
					}
					want, err := probeLibopusLPCNetFeatures(frames[:(frame+1)*FrameSize])
					if err != nil {
						t.Fatalf("selected C features: %v", err)
					}
					for i, value := range analysis.xcorrFeatures {
						if math.Float32bits(value) != math.Float32bits(want.XCorr[i]) {
							t.Fatalf("xcorr[%d] Go=%08x C=%08x", i, math.Float32bits(value), math.Float32bits(want.XCorr[i]))
						}
					}
					if math.Float32bits(analysis.dnnPitch) != math.Float32bits(want.DNNPitch) {
						t.Fatalf("dnn_pitch Go=%08x C=%08x", math.Float32bits(analysis.dnnPitch), math.Float32bits(want.DNNPitch))
					}
					base := frame * NumTotalFeatures
					for i, value := range got {
						if math.Float32bits(value) != math.Float32bits(want.Features[base+i]) {
							t.Fatalf("feature[%d] Go=%08x C=%08x", i, math.Float32bits(value), math.Float32bits(want.Features[base+i]))
						}
					}
				})
			}

			var got [NumTotalFeatures]float32
			input := frames[len(frames)-FrameSize:]
			analysis.ComputeSingleFrameFeaturesFloat(got[:], input)
			if allocs := testing.AllocsPerRun(100, func() {
				analysis.ComputeSingleFrameFeaturesFloat(got[:], input)
			}); allocs != 0 {
				t.Fatalf("warm analysis allocations=%g want 0", allocs)
			}
		})
	}
}
