//go:build gopus_osce

package lpcnetplc

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestAnalysisBandEnergyMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	raw, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		t.Fatalf("PitchDNN model oracle: %v", err)
	}
	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, frameSize := range []int{1920, 2880} {
		t.Run(fmt.Sprintf("%d_samples", frameSize), func(t *testing.T) {
			var analysis Analysis
			analysis.SetDREDEncoderMode(true)
			if err := analysis.SetModel(blob); err != nil {
				t.Fatal(err)
			}
			frames := dredParityAnalysisFrames(4, frameSize)
			for frame := 0; frame < len(frames)/FrameSize; frame++ {
				t.Run(fmt.Sprintf("frame_%02d", frame), func(t *testing.T) {
					pcm := frames[frame*FrameSize : (frame+1)*FrameSize]
					var preWindow [analysisWindowSize]float32
					copy(preWindow[:analysisOverlapSize], analysis.analysisMem[:])
					copy(preWindow[analysisOverlapSize:], pcm)
					mem := analysis.memPreemph
					preemphasisInPlace(preWindow[analysisOverlapSize:], &mem, analysisPreemphasis)
					want, err := probeLibopusAnalysisStages(preWindow[:])
					if err != nil {
						t.Fatalf("selected libopus band energy: %v", err)
					}
					var got [NumBands]float32
					computeBandEnergy(got[:], want.Spectrum, true)
					for i, v := range got {
						if gotBits, wantBits := math.Float32bits(v), math.Float32bits(want.BandE[i]); gotBits != wantBits {
							t.Fatalf("band energy[%d]=0x%08x want 0x%08x", i, gotBits, wantBits)
						}
					}
					var features [NumTotalFeatures]float32
					analysis.ComputeSingleFrameFeaturesFloat(features[:], pcm)
				})
			}
		})
	}
}

func TestAnalysisBandEnergyWarmZeroAlloc(t *testing.T) {
	var spectrum [analysisFreqSize]complex64
	var bands [NumBands]float32
	for i := range spectrum {
		spectrum[i] = complex(float32((i%17)-8)/32, float32((i%7)-3)/64)
	}
	call := func() { computeBandEnergy(bands[:], spectrum[:], true) }
	call()
	if allocs := testing.AllocsPerRun(1000, call); allocs != 0 {
		t.Fatalf("band energy warm allocations=%g want 0", allocs)
	}
}
