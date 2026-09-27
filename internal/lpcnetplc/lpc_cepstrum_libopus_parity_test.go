//go:build gopus_osce

package lpcnetplc

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestLPCFromCepstrumMatchesSelectedLibopus(t *testing.T) {
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
						t.Fatalf("selected libopus LPC stages: %v", err)
					}
					var got [analysisLPCOrder]float32
					var scratch analysisScratch
					lpcFromCepstrum(got[:], want.Features, &scratch)
					for i, v := range got {
						if gotBits, wantBits := math.Float32bits(v), math.Float32bits(want.LPC[i]); gotBits != wantBits {
							t.Fatalf("LPC[%d]=0x%08x want 0x%08x", i, gotBits, wantBits)
						}
					}
					var features [NumTotalFeatures]float32
					analysis.ComputeSingleFrameFeaturesFloat(features[:], pcm)
				})
			}
		})
	}
}

func TestLPCFromCepstrumWarmZeroAlloc(t *testing.T) {
	var cepstrum [NumBands]float32
	var lpc [analysisLPCOrder]float32
	var scratch analysisScratch
	for i := range cepstrum {
		cepstrum[i] = float32((i%5)-2) / 8
	}
	call := func() { lpcFromCepstrum(lpc[:], cepstrum[:], &scratch) }
	call()
	if allocs := testing.AllocsPerRun(1000, call); allocs != 0 {
		t.Fatalf("lpcFromCepstrum warm allocations=%g want 0", allocs)
	}
}
